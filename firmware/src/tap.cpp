#include "tap.h"

#include <Arduino.h>
#include <mbedtls/base64.h>

#include "probe_config.h"

namespace tap {
namespace {

HardwareSerial port(1); // UART1; UART0 is our own log port
std::string window;     // bytes kept this window (up to TAP_WINDOW_BYTES)
Stats st{};
uint8_t last[8];
size_t lastCount = 0;

void remember(uint8_t b) {
  if (lastCount == sizeof(last)) {
    memmove(last, last + 1, sizeof(last) - 1);
    lastCount--;
  }
  last[lastCount++] = b;
}

} // namespace

void begin() {
  port.setRxBufferSize(TAP_RX_BUFFER);
  // RX only: TX pin -1, so we can never drive their line.
  port.begin(TAP_BAUD, SERIAL_8N1, TAP_RX_PIN, -1);
  window.reserve(TAP_WINDOW_BYTES);
}

void poll() {
  uint8_t chunk[256];
  int available = port.available();
  while (available > 0) {
    const size_t n = port.read(chunk, available < static_cast<int>(sizeof(chunk)) ? available : sizeof(chunk));
    if (n == 0) break;
    for (size_t i = 0; i < n; ++i) {
      remember(chunk[i]);
      if (window.size() < TAP_WINDOW_BYTES) window.push_back(static_cast<char>(chunk[i]));
      else st.overflows++;
    }
    st.bytesThisWindow += n;
    st.bytesTotal += n;
    available = port.available();
  }
}

std::string finishWindow() {
  poll();
  // base64 of the kept bytes
  size_t outLen = 0;
  std::string b64((window.size() + 2) / 3 * 4 + 1, '\0');
  mbedtls_base64_encode(reinterpret_cast<unsigned char *>(&b64[0]), b64.size(), &outLen,
                        reinterpret_cast<const unsigned char *>(window.data()), window.size());
  b64.resize(outLen);

  std::string json = "{\"pin\":" + std::to_string(TAP_RX_PIN) +
                     ",\"baud\":" + std::to_string(TAP_BAUD) +
                     ",\"bytes\":" + std::to_string(st.bytesThisWindow) +
                     ",\"kept\":" + std::to_string(window.size());
  if (st.overflows) json += ",\"dropped\":" + std::to_string(st.overflows);
  json += ",\"data\":\"" + b64 + "\"}";

  // last bytes as hex, for the screen
  size_t pos = 0;
  for (size_t i = 0; i < lastCount; ++i) {
    pos += snprintf(st.lastHex + pos, sizeof(st.lastHex) - pos, i ? " %02X" : "%02X", last[i]);
  }
  st.lastHex[pos] = '\0';

  window.clear();
  st.lastWindowBytes = st.bytesThisWindow;
  st.bytesThisWindow = 0;
  st.overflows = 0;
  return json;
}

Stats stats() { return st; }

} // namespace tap
