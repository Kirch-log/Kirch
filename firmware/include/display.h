#pragma once
// The module's two OLED screens (SSD1306 128x64, one per I2C bus).
// A: status (device, Wi-Fi, clients, tap). B: live probe readings.
// Drawing runs in its own task, so a slow or missing screen never delays
// measuring.

#include <stdint.h>
#include <string>

namespace display {

struct Status {
  char line[4][22]; // 4 lines of up to 21 characters on screen A
};

void begin();

// Screen A text (copied; safe to call from loop()).
void setStatus(const Status &status);

// Screen B: one capture's "probes" JSON + tap byte count.
void setReadings(const std::string &probesJson, uint32_t tapBytes);

} // namespace display
