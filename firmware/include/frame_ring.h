#pragma once
// Keeps the last N sent frames so a client that missed some (Wi-Fi drop)
// can ask for them again by sequence number. Pure C++, unit-tested on laptop.

#include <stddef.h>
#include <stdint.h>
#include <string>

template <size_t N>
class FrameRing {
public:
  void push(uint32_t seq, const std::string &frame) {
    Slot &slot = slots_[seq % N];
    slot.seq = seq;
    slot.frame = frame;
    slot.used = true;
    if (count_ < N) ++count_;
    newest_ = seq;
  }

  // Oldest sequence still held, or 0 if empty.
  uint32_t oldest() const {
    if (count_ == 0) return 0;
    return newest_ - static_cast<uint32_t>(count_) + 1;
  }
  uint32_t newest() const { return count_ ? newest_ : 0; }

  // Calls fn(seq, frame) for every held frame with seq >= from, in order.
  template <typename Fn>
  void forEachFrom(uint32_t from, Fn fn) const {
    if (count_ == 0) return;
    uint32_t start = from < oldest() ? oldest() : from;
    for (uint32_t seq = start; seq <= newest_; ++seq) {
      const Slot &slot = slots_[seq % N];
      if (slot.used && slot.seq == seq) fn(seq, slot.frame);
    }
  }

private:
  struct Slot {
    uint32_t seq = 0;
    std::string frame;
    bool used = false;
  };
  Slot slots_[N];
  size_t count_ = 0;
  uint32_t newest_ = 0;
};
