// Laptop tests for the parts of the firmware that don't need the ESP32.
//   g++ -std=c++17 -Wall -Wextra -Iinclude test/test_host.cpp -o test_host && ./test_host

#include <cstdio>
#include <string>
#include <vector>

#include "frame_ring.h"
#include "stats.h"

static int failures = 0;
#define CHECK(cond)                                                    \
  do {                                                                 \
    if (!(cond)) {                                                     \
      std::printf("FAIL %s:%d  %s\n", __FILE__, __LINE__, #cond);      \
      ++failures;                                                      \
    }                                                                  \
  } while (0)

static void testAnalog() {
  const uint16_t samples[] = {4900, 5000, 4950, 5010};
  auto s = stats::summarizeAnalog(samples, 4);
  CHECK(s.valid);
  CHECK(s.minMv == 4900);
  CHECK(s.maxMv == 5010);
  CHECK(s.avgMv == 4965);
  CHECK(!stats::summarizeAnalog(samples, 0).valid);
}

// Servo-like signal: 50 Hz (20000 us period), 1500 us high.
static void testServoPulse() {
  std::vector<uint32_t> t;
  std::vector<uint8_t> lvl;
  for (uint32_t k = 0; k < 50; ++k) {
    t.push_back(1000 + k * 20000);
    lvl.push_back(1);
    t.push_back(1000 + k * 20000 + 1500);
    lvl.push_back(0);
  }
  auto s = stats::summarizePulse<512>(t.data(), lvl.data(), t.size());
  CHECK(s.edges == 100);
  CHECK(s.hasPeriod && s.periodUs == 20000);
  CHECK(s.hasHigh && s.highUs == 1500);
}

// Window starts mid-pulse (first edge is falling): that half pulse is ignored.
static void testCutPulse() {
  const uint32_t t[] = {500, 10000, 10010, 110000, 110010};
  const uint8_t lvl[] = {0, 1, 0, 1, 0};
  auto s = stats::summarizePulse<512>(t, lvl, 5);
  CHECK(s.edges == 5);
  CHECK(s.hasPeriod && s.periodUs == 100000);
  CHECK(s.hasHigh && s.highUs == 10);
}

static void testNoEdges() {
  auto s = stats::summarizePulse<512>(nullptr, nullptr, 0);
  CHECK(s.edges == 0);
  CHECK(!s.hasPeriod);
  CHECK(!s.hasHigh);
}

// Median ignores one glitch.
static void testGlitch() {
  const uint32_t t[] = {0, 1500, 20000, 21500, 40000, 40003, 60000, 61500};
  const uint8_t lvl[] = {1, 0, 1, 0, 1, 0, 1, 0};
  auto s = stats::summarizePulse<512>(t, lvl, 8);
  CHECK(s.highUs == 1500);
  CHECK(s.periodUs == 20000);
}

static void testRing() {
  FrameRing<4> ring;
  CHECK(ring.oldest() == 0 && ring.newest() == 0);
  for (uint32_t seq = 1; seq <= 6; ++seq) ring.push(seq, "f" + std::to_string(seq));
  CHECK(ring.oldest() == 3);
  CHECK(ring.newest() == 6);

  std::vector<uint32_t> got;
  ring.forEachFrom(1, [&](uint32_t seq, const std::string &f) {
    got.push_back(seq);
    CHECK(f == "f" + std::to_string(seq));
  });
  CHECK((got == std::vector<uint32_t>{3, 4, 5, 6}));

  got.clear();
  ring.forEachFrom(5, [&](uint32_t seq, const std::string &) { got.push_back(seq); });
  CHECK((got == std::vector<uint32_t>{5, 6}));

  got.clear();
  ring.forEachFrom(7, [&](uint32_t seq, const std::string &) { got.push_back(seq); });
  CHECK(got.empty());
}

int main() {
  testAnalog();
  testServoPulse();
  testCutPulse();
  testNoEdges();
  testGlitch();
  testRing();
  if (failures) {
    std::printf("%d check(s) failed\n", failures);
    return 1;
  }
  std::printf("all host tests passed\n");
  return 0;
}
