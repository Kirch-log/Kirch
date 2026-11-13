#include "capture.h"

#include <Arduino.h>
#include <ArduinoJson.h>
#include "esp_timer.h"
#include "soc/gpio_reg.h"

#include "probe_config.h"
#include "stats.h"

namespace capture {
namespace {

// Edge log per probe, double-buffered: the interrupt writes into buffer
// `active`, and at the end of a window loop() flips `active` (O(1) inside a
// critical section) and reads the other buffer at leisure.
struct EdgeBuffer {
  uint32_t timesUs[MAX_EDGES];
  uint8_t levels[MAX_EDGES];
  uint16_t count;
  uint16_t dropped;
};

struct ProbeState {
  EdgeBuffer buffers[2];
  volatile uint8_t active;
  uint16_t analog[ANALOG_SAMPLES];
  size_t analogCount;
};

ProbeState probes[PROBE_COUNT];
portMUX_TYPE edgeMux = portMUX_INITIALIZER_UNLOCKED;
// Pin numbers copied into RAM: the ISR may run while flash is busy
// (e.g. Wi-Fi saving credentials), so it must not read PROBES from flash.
DRAM_ATTR uint8_t isrPins[PROBE_COUNT];

// Read a GPIO level straight from the register (IRAM-safe, unlike digitalRead).
inline uint8_t IRAM_ATTR readLevel(uint8_t pin) {
  return pin < 32 ? (REG_READ(GPIO_IN_REG) >> pin) & 1
                  : (REG_READ(GPIO_IN1_REG) >> (pin - 32)) & 1;
}

uint32_t windowStartMs = 0;
uint32_t nextAnalogMs = 0;

void IRAM_ATTR onEdge(void *arg) {
  const size_t index = reinterpret_cast<size_t>(arg);
  const uint32_t nowUs = static_cast<uint32_t>(esp_timer_get_time());
  const uint8_t level = readLevel(isrPins[index]);
  portENTER_CRITICAL_ISR(&edgeMux);
  EdgeBuffer &buf = probes[index].buffers[probes[index].active];
  if (buf.count < MAX_EDGES) {
    buf.timesUs[buf.count] = nowUs;
    buf.levels[buf.count] = level;
    ++buf.count;
  } else {
    ++buf.dropped;
  }
  portEXIT_CRITICAL_ISR(&edgeMux);
}

void sampleAnalog() {
  for (size_t i = 0; i < PROBE_COUNT; ++i) {
    if (PROBES[i].mode != ProbeMode::Analog) continue;
    ProbeState &p = probes[i];
    if (p.analogCount < ANALOG_SAMPLES) {
      p.analog[p.analogCount++] = static_cast<uint16_t>(analogReadMilliVolts(PROBES[i].pin));
    }
  }
}

// Flip every probe's edge buffer; returns the index of the buffer that just
// finished (now safe to read, the ISR writes to the other one).
uint8_t swapBuffers() {
  portENTER_CRITICAL(&edgeMux);
  const uint8_t finished = probes[0].active;
  for (size_t i = 0; i < PROBE_COUNT; ++i) {
    const uint8_t next = probes[i].active ^ 1;
    probes[i].buffers[next].count = 0;
    probes[i].buffers[next].dropped = 0;
    probes[i].active = next;
  }
  portEXIT_CRITICAL(&edgeMux);
  return finished;
}

} // namespace

void begin() {
  analogReadResolution(12);
  for (size_t i = 0; i < PROBE_COUNT; ++i) {
    const ProbeConfig &cfg = PROBES[i];
    isrPins[i] = cfg.pin;
    if (cfg.mode == ProbeMode::Analog) {
      pinMode(cfg.pin, INPUT);
    } else {
      pinMode(cfg.pin, INPUT); // passive probe: no pull-up/down on the target's signal
      attachInterruptArg(cfg.pin, onEdge, reinterpret_cast<void *>(i), CHANGE);
    }
  }
  windowStartMs = millis();
  nextAnalogMs = windowStartMs;
}

bool poll(uint32_t nowMs, std::string &probesJson) {
  if (static_cast<int32_t>(nowMs - nextAnalogMs) >= 0) {
    sampleAnalog();
    nextAnalogMs += WINDOW_MS / ANALOG_SAMPLES;
  }
  if (nowMs - windowStartMs < WINDOW_MS) return false;

  const uint8_t finished = swapBuffers();
  windowStartMs = nowMs;
  nextAnalogMs = nowMs;

  JsonDocument doc;
  for (size_t i = 0; i < PROBE_COUNT; ++i) {
    const ProbeConfig &cfg = PROBES[i];
    ProbeState &p = probes[i];
    JsonObject out = doc[cfg.id].to<JsonObject>();
    out["mode"] = modeName(cfg.mode);
    out["pin"] = cfg.pin;

    if (cfg.mode == ProbeMode::Analog) {
      const stats::AnalogSummary s = stats::summarizeAnalog(p.analog, p.analogCount);
      p.analogCount = 0;
      if (s.valid) {
        JsonObject mv = out["mv"].to<JsonObject>();
        mv["min"] = s.minMv;
        mv["max"] = s.maxMv;
        mv["avg"] = s.avgMv;
      }
      continue;
    }

    const EdgeBuffer &buf = p.buffers[finished];
    if (buf.dropped) out["dropped_edges"] = buf.dropped;

    if (cfg.mode == ProbeMode::Digital) {
      out["state"] = digitalRead(cfg.pin);
      out["transitions"] = buf.count;
    } else {
      const stats::PulseSummary s =
          stats::summarizePulse<MAX_EDGES>(buf.timesUs, buf.levels, buf.count);
      out["edges"] = s.edges;
      if (s.hasPeriod) out["period_us"] = s.periodUs;
      if (s.hasHigh) out["high_us"] = s.highUs;
      out["state"] = digitalRead(cfg.pin);
    }
  }
  probesJson.clear();
  serializeJson(doc, probesJson);
  return true;
}

} // namespace capture
