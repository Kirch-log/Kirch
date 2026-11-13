#pragma once
// Measures all probes over one window and builds a "capture" frame.

#include <stdint.h>
#include <string>

namespace capture {

void begin();

// Call often from loop(). Returns true once per window, with the finished
// readings written as the "probes" object of a frame into `probesJson`.
bool poll(uint32_t nowMs, std::string &probesJson);

} // namespace capture
