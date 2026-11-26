#include "display.h"

#include <Adafruit_GFX.h>
#include <Adafruit_SSD1306.h>
#include <Arduino.h>
#include <ArduinoJson.h>
#include <Wire.h>

#include "probe_config.h"

namespace display {
namespace {

TwoWire busA(0);
TwoWire busB(1);
Adafruit_SSD1306 screenA(128, 64, &busA, -1);
Adafruit_SSD1306 screenB(128, 64, &busB, -1);
bool readyA = false, readyB = false;

struct Readings {
  char header[22];
  char line[PROBE_COUNT][22];
};

SemaphoreHandle_t lock = nullptr;
Status pendingStatus{};
Readings pendingReadings{};
bool dirty = true;

bool present(TwoWire &bus) {
  bus.beginTransmission(OLED_ADDRESS);
  return bus.endTransmission() == 0;
}

bool start(Adafruit_SSD1306 &screen, TwoWire &bus) {
  return present(bus) && screen.begin(SSD1306_SWITCHCAPVCC, OLED_ADDRESS, false, false);
}

void drawStatus(const Status &s) {
  if (!readyA) return;
  screenA.clearDisplay();
  screenA.fillRect(0, 0, 128, 12, SSD1306_WHITE);
  screenA.setTextColor(SSD1306_BLACK);
  screenA.setCursor(2, 2);
  screenA.print("KIRCH PROBE");
  screenA.setTextColor(SSD1306_WHITE);
  for (int i = 0; i < 4; ++i) {
    screenA.setCursor(0, 16 + i * 12);
    screenA.print(s.line[i]);
  }
  screenA.display();
}

void drawReadings(const Readings &r) {
  if (!readyB) return;
  screenB.clearDisplay();
  screenB.fillRect(0, 0, 128, 9, SSD1306_WHITE);
  screenB.setTextColor(SSD1306_BLACK);
  screenB.setCursor(1, 1);
  screenB.print(r.header);
  screenB.setTextColor(SSD1306_WHITE);
  for (size_t i = 0; i < PROBE_COUNT; ++i) {
    screenB.setCursor(0, 10 + i * 9);
    screenB.print(r.line[i]);
  }
  screenB.display();
}

void task(void *) {
  for (;;) {
    Status s;
    Readings r;
    bool draw = false;
    xSemaphoreTake(lock, portMAX_DELAY);
    if (dirty) {
      s = pendingStatus;
      r = pendingReadings;
      dirty = false;
      draw = true;
    }
    xSemaphoreGive(lock);
    if (draw) {
      drawStatus(s);
      drawReadings(r);
    }
    vTaskDelay(pdMS_TO_TICKS(200));
  }
}

void formatProbe(char *out, size_t size, const char *id, JsonObjectConst p) {
  const char *mode = p["mode"] | "";
  if (strcmp(mode, "analog") == 0) {
    if (p["mv"].is<JsonObjectConst>()) snprintf(out, size, "%s %5umV", id, p["mv"]["avg"].as<unsigned>());
    else snprintf(out, size, "%s  --", id);
  } else if (strcmp(mode, "pulse") == 0) {
    const unsigned period = p["period_us"] | 0u;
    const unsigned high = p["high_us"] | 0u;
    if (period) snprintf(out, size, "%s %4uHz %5uus", id, 1000000u / period, high);
    else snprintf(out, size, "%s %s %u edges", id, (p["state"] | 0) ? "HI" : "LO", p["edges"] | 0u);
  } else {
    snprintf(out, size, "%s %s %u flips", id, (p["state"] | 0) ? "HIGH" : "LOW", p["transitions"] | 0u);
  }
}

} // namespace

void begin() {
  lock = xSemaphoreCreateMutex();
  busA.begin(OLED_A_SDA, OLED_A_SCL, 400000);
  busB.begin(OLED_B_SDA, OLED_B_SCL, 400000);
  readyA = start(screenA, busA);
  readyB = start(screenB, busB);
  Serial.printf("[oled] screen A %s, screen B %s\n", readyA ? "ok" : "not found", readyB ? "ok" : "not found");
  snprintf(pendingReadings.header, sizeof(pendingReadings.header), "LIVE  (waiting)");
  for (size_t i = 0; i < PROBE_COUNT; ++i) snprintf(pendingReadings.line[i], 22, "%s  --", PROBES[i].id);
  // Core 0: Wi-Fi also lives there, but drawing is short; measuring stays on core 1.
  xTaskCreatePinnedToCore(task, "oled", 4096, nullptr, 1, nullptr, 0);
}

void setStatus(const Status &status) {
  if (!lock) return;
  xSemaphoreTake(lock, portMAX_DELAY);
  pendingStatus = status;
  dirty = true;
  xSemaphoreGive(lock);
}

void setReadings(const std::string &probesJson, uint32_t tapBytes) {
  if (!lock) return;
  Readings r{};
  snprintf(r.header, sizeof(r.header), "LIVE  tap %luB/s", static_cast<unsigned long>(tapBytes));
  JsonDocument doc;
  if (deserializeJson(doc, probesJson) == DeserializationError::Ok) {
    for (size_t i = 0; i < PROBE_COUNT; ++i) formatProbe(r.line[i], 22, PROBES[i].id, doc[PROBES[i].id]);
  }
  xSemaphoreTake(lock, portMAX_DELAY);
  pendingReadings = r;
  dirty = true;
  xSemaphoreGive(lock);
}

} // namespace display
