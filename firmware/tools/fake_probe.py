"""Pretend to be a Kirch probe, so the laptop side can be tested without the ESP32.

    python firmware/tools/fake_probe.py            # ws://localhost:8081/
    python firmware/tools/fake_probe.py --drop 0.2 # "lose" 20% of frames over Wi-Fi

Speaks the same messages as firmware/src/main.cpp: hello, capture, gap, resend.
Simulates the ultrasonic + servo circuit from docs/formats.md.
"""

import argparse
import asyncio
import json
import random
import time

from websockets.asyncio.server import serve

WINDOW_MS = 1000
BUFFER = 32
PROBES = {
    "P1": (8, "analog"),
    "P2": (3, "pulse"),
    "P3": (16, "pulse"),
    "P4": (21, "pulse"),
    "P5": (9, "analog"),
    "P6": (48, "digital"),
}


class FakeProbe:
    def __init__(self, drop):
        self.drop = drop
        self.boot_id = random.getrandbits(32)
        self.seq = 0
        self.frames = {}  # seq -> json text, last BUFFER kept
        self.clients = set()
        self.started = time.monotonic()

    def reading(self):
        jitter = lambda v, d: v + random.randint(-d, d)
        return {
            "P1": {"pin": 8, "mode": "analog", "mv": {"min": jitter(4930, 10), "max": jitter(5010, 10), "avg": jitter(4975, 5)}},
            "P2": {"pin": 3, "mode": "pulse", "edges": 20, "period_us": jitter(100000, 50), "high_us": jitter(10, 1), "state": 0},
            "P3": {"pin": 16, "mode": "pulse", "edges": 20, "period_us": jitter(100000, 50), "high_us": jitter(1160, 40), "state": 0},
            "P4": {"pin": 21, "mode": "pulse", "edges": 100, "period_us": jitter(20000, 5), "high_us": jitter(1500, 3), "state": 0},
            "P5": {"pin": 9, "mode": "analog", "mv": {"min": 0, "max": 3, "avg": 1}},
            "P6": {"pin": 48, "mode": "digital", "state": 0, "transitions": 0},
        }

    def hello(self):
        held = sorted(self.frames)
        return json.dumps({
            "type": "hello",
            "device": {"id": "kirch-probe-fake01", "firmware": "fake"},
            "boot_id": self.boot_id,
            "window_ms": WINDOW_MS,
            "newest_seq": held[-1] if held else 0,
            "oldest_seq": held[0] if held else 0,
            "probes": {pid: {"pin": pin, "mode": mode} for pid, (pin, mode) in PROBES.items()},
        })

    async def handler(self, ws):
        self.clients.add(ws)
        print("client connected")
        try:
            await ws.send(self.hello())
            async for raw in ws:
                msg = json.loads(raw)
                if msg.get("type") == "resend":
                    start = msg.get("from", 0)
                    oldest = min(self.frames) if self.frames else 0
                    if self.frames and start < oldest:
                        await ws.send(json.dumps({"type": "gap", "from": start, "oldest": oldest}))
                    for seq in sorted(s for s in self.frames if s >= start):
                        await ws.send(self.frames[seq])
                    print(f"re-sent from #{start}")
        finally:
            self.clients.discard(ws)
            print("client disconnected")

    async def produce(self):
        while True:
            await asyncio.sleep(WINDOW_MS / 1000)
            self.seq += 1
            frame = json.dumps({
                "type": "capture",
                "seq": self.seq,
                "boot_id": self.boot_id,
                "device_id": "kirch-probe-fake01",
                "uptime_ms": int((time.monotonic() - self.started) * 1000),
                "unix_ms": int(time.time() * 1000),
                "window_ms": WINDOW_MS,
                "probes": self.reading(),
            })
            self.frames[self.seq] = frame
            self.frames.pop(self.seq - BUFFER, None)
            if random.random() < self.drop:
                print(f"dropped #{self.seq} (pretend Wi-Fi loss)")
                continue
            for ws in list(self.clients):
                try:
                    await ws.send(frame)
                except Exception:
                    pass


async def main(port, drop, seconds):
    probe = FakeProbe(drop)
    async with serve(probe.handler, "localhost", port):
        print(f"fake probe on ws://localhost:{port}/", flush=True)
        if seconds:
            try:
                await asyncio.wait_for(probe.produce(), timeout=seconds)
            except asyncio.TimeoutError:
                print("fake probe stopping")
        else:
            await probe.produce()


if __name__ == "__main__":
    parser = argparse.ArgumentParser(description=__doc__.splitlines()[0])
    parser.add_argument("--port", type=int, default=8081)
    parser.add_argument("--drop", type=float, default=0.0, help="fraction of frames to drop (0-1)")
    parser.add_argument("--seconds", type=float, default=0, help="stop after this long (0 = run forever)")
    args = parser.parse_args()
    try:
        asyncio.run(main(args.port, args.drop, args.seconds))
    except KeyboardInterrupt:
        pass
