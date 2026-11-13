#pragma once
// Pure C++ (no Arduino) so it can be unit-tested on the laptop:
//   g++ -std=c++17 -Iinclude test/test_stats.cpp -o test_stats && ./test_stats

#include <algorithm>
#include <stddef.h>
#include <stdint.h>

namespace stats {

// Median of up to `n` values. Sorts a copy; n is small (<= MAX_EDGES).
template <size_t Cap>
inline bool median(const uint32_t *values, size_t n, uint32_t &out) {
  if (n == 0) return false;
  static uint32_t copy[Cap]; // static: keeps big arrays off the small loop() stack
  if (n > Cap) n = Cap;
  std::copy(values, values + n, copy);
  std::nth_element(copy, copy + n / 2, copy + n);
  out = copy[n / 2];
  return true;
}

struct AnalogSummary {
  uint16_t minMv = 0, maxMv = 0, avgMv = 0;
  bool valid = false;
};

inline AnalogSummary summarizeAnalog(const uint16_t *samples, size_t n) {
  AnalogSummary s;
  if (n == 0) return s;
  uint32_t sum = 0;
  s.minMv = samples[0];
  s.maxMv = samples[0];
  for (size_t i = 0; i < n; ++i) {
    s.minMv = std::min(s.minMv, samples[i]);
    s.maxMv = std::max(s.maxMv, samples[i]);
    sum += samples[i];
  }
  s.avgMv = static_cast<uint16_t>((sum + n / 2) / n);
  s.valid = true;
  return s;
}

struct PulseSummary {
  uint32_t edges = 0;
  uint32_t periodUs = 0; // median rising-to-rising
  uint32_t highUs = 0;   // median rising-to-falling
  bool hasPeriod = false;
  bool hasHigh = false;
};

// Edges recorded in one window: timestamp (µs) and the level *after* the edge
// (1 = rising, 0 = falling). Only intervals with both ends inside the window
// are measured, so a pulse cut by the window boundary is never half-counted.
template <size_t Cap>
inline PulseSummary summarizePulse(const uint32_t *timesUs, const uint8_t *levels, size_t n) {
  PulseSummary s;
  s.edges = static_cast<uint32_t>(n);
  // static: not reentrant, only called from the capture loop.
  static uint32_t periods[Cap];
  static uint32_t highs[Cap];
  size_t periodCount = 0, highCount = 0;
  bool haveRise = false;
  uint32_t lastRise = 0;
  for (size_t i = 0; i < n && i < Cap; ++i) {
    if (levels[i]) {
      if (haveRise) periods[periodCount++] = timesUs[i] - lastRise;
      lastRise = timesUs[i];
      haveRise = true;
    } else if (haveRise && i > 0 && levels[i - 1]) {
      // falling edge directly after a rising edge = one HIGH pulse
      highs[highCount++] = timesUs[i] - lastRise;
    }
  }
  s.hasPeriod = median<Cap>(periods, periodCount, s.periodUs);
  s.hasHigh = median<Cap>(highs, highCount, s.highUs);
  return s;
}

} // namespace stats
