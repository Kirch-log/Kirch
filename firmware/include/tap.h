#pragma once
// Serial tap: listens to the user's board's serial line (TX) on one input
// pin. Read only: nothing is ever sent to their board.

#include <stdint.h>
#include <string>

namespace tap {

struct Stats {
  uint32_t bytesThisWindow;
  uint32_t lastWindowBytes; // bytes in the window finishWindow() just closed
  uint32_t bytesTotal;
  uint32_t overflows;     // bytes lost because the receive buffer was full
  char lastHex[3 * 8 + 1]; // last bytes seen, "4F 4B 0A"
};

void begin();

// Call often from loop(): moves received bytes into this window's buffer.
void poll();

// At the end of a window: the "tap" JSON object for the capture frame
// (pin, baud, byte count, data as base64), then starts a new window.
std::string finishWindow();

Stats stats();

} // namespace tap
