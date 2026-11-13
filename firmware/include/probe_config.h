#pragma once
// Probe pin map and timing for the ESP32-S3 probe board.
// Pins match the S3 board from the hackathon build, except P4 is now
// "pulse" (servo PWM) instead of "digital".

#include <stdint.h>
#include <stddef.h>

enum class ProbeMode : uint8_t { Analog, Digital, Pulse };

struct ProbeConfig {
  const char *id;
  uint8_t pin;
  ProbeMode mode;
};

// Avoided on the S3: GPIO19/20 (USB), 26-37 (flash/PSRAM), 43/44 (UART0),
// 0/45/46 (strapping). Analog probes use ADC1 only (ADC2 conflicts with Wi-Fi).
static constexpr ProbeConfig PROBES[] = {
    {"P1", 8, ProbeMode::Analog},   // ADC1
    {"P2", 3, ProbeMode::Pulse},
    {"P3", 16, ProbeMode::Pulse},
    {"P4", 21, ProbeMode::Pulse},
    {"P5", 9, ProbeMode::Analog},   // ADC1
    {"P6", 48, ProbeMode::Digital}, // RGB LED on some DevKitC-1 v1.0 boards
};
static constexpr size_t PROBE_COUNT = sizeof(PROBES) / sizeof(PROBES[0]);

static constexpr uint32_t WINDOW_MS = 1000;        // one capture per second
static constexpr size_t ANALOG_SAMPLES = 32;       // per window
static constexpr size_t MAX_EDGES = 512;           // per probe per window
static constexpr size_t RESEND_BUFFER_FRAMES = 32; // last N frames kept for re-send

static constexpr const char *MDNS_HOSTNAME = "kirch-probe"; // kirch-probe.local
static constexpr uint16_t WS_PORT = 81;

// BLE provisioning: phone app "ESP BLE Provisioning" (Espressif).
// Proof-of-possession code the app asks for. Change per device before a demo.
static constexpr const char *PROV_POP = "kirch1234";
// Hold BOOT (GPIO0) during power-up for 3 s to forget saved Wi-Fi.
static constexpr uint8_t RESET_BUTTON_PIN = 0;
static constexpr uint32_t RESET_HOLD_MS = 3000;

static inline const char *modeName(ProbeMode mode) {
  switch (mode) {
  case ProbeMode::Analog: return "analog";
  case ProbeMode::Digital: return "digital";
  case ProbeMode::Pulse: return "pulse";
  }
  return "unknown";
}
