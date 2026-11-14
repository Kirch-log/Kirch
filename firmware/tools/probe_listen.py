"""Connect to a Kirch probe over Wi-Fi and print its readings.

    pip install -r firmware/tools/requirements.txt
    python firmware/tools/probe_listen.py                       # real probe
    python firmware/tools/probe_listen.py --url ws://localhost:8081/   # fake probe
    python firmware/tools/probe_listen.py --save capture.json --count 1

Asks the probe to re-send any frames it missed (numbered by "seq").
--save writes the last reading in the kirch.capture/1 format (docs/formats.md),
without "net" fields; the kirch CLI will add those later.
"""

import argparse
import asyncio
import json
from datetime import datetime, timezone

from websockets.asyncio.client import connect


def to_capture(frame, device):
    """Turn a probe 'capture' frame into the capture.json layout."""
    if frame.get("unix_ms"):
        when = datetime.fromtimestamp(frame["unix_ms"] / 1000, tz=timezone.utc)
    else:
        when = datetime.now(timezone.utc)
    probes = {}
    for probe_id, reading in frame["probes"].items():
        probes[probe_id] = {k: v for k, v in reading.items() if k != "pin"}
    return {
        "format": "kirch.capture/1",
        "device": device,
        "captured_at": when.strftime("%Y-%m-%dT%H:%M:%SZ"),
        "window_ms": frame["window_ms"],
        "probes": probes,
    }


def describe(reading):
    mode = reading.get("mode")
    if mode == "analog":
        mv = reading.get("mv")
        return f"{mv['avg']} mV ({mv['min']}-{mv['max']})" if mv else "no samples"
    if mode == "digital":
        return f"state {reading.get('state')}, {reading.get('transitions')} transitions"
    if mode == "pulse":
        parts = [f"{reading.get('edges', 0)} edges"]
        if "period_us" in reading:
            parts.append(f"period {reading['period_us']} us")
        if "high_us" in reading:
            parts.append(f"high {reading['high_us']} us")
        return ", ".join(parts)
    return json.dumps(reading)


async def listen(url, count, save):
    device = None
    boot_id = None
    last_seq = 0
    received = 0
    async with connect(url, open_timeout=10) as ws:
        print(f"connected to {url}")
        async for raw in ws:
            msg = json.loads(raw)
            kind = msg.get("type")

            if kind == "hello":
                device = msg["device"]
                print(f"probe {device['id']} firmware {device['firmware']}, "
                      f"window {msg['window_ms']} ms")
                for pid, p in msg["probes"].items():
                    print(f"  {pid}: GPIO{p['pin']} {p['mode']}")
                continue

            if kind == "gap":
                print(f"! frames {msg['from']}..{msg['oldest'] - 1} are gone (probe buffer too small)")
                continue

            if kind != "capture":
                continue

            if msg["boot_id"] != boot_id:
                if boot_id is not None:
                    print("! probe restarted, numbering starts again")
                boot_id = msg["boot_id"]
                last_seq = msg["seq"] - 1

            seq = msg["seq"]
            if seq <= last_seq:
                continue  # duplicate from a re-send
            if seq > last_seq + 1:
                print(f"! missed {last_seq + 1}..{seq - 1}, asking for them again")
                await ws.send(json.dumps({"type": "resend", "from": last_seq + 1}))
                continue  # the re-send repeats this frame too, in order
            last_seq = seq

            print(f"#{seq}")
            for pid, reading in msg["probes"].items():
                print(f"  {pid:<3} {describe(reading)}")

            if save and device:
                with open(save, "w", encoding="utf-8") as f:
                    json.dump(to_capture(msg, device), f, indent=2, sort_keys=True)
                    f.write("\n")

            received += 1
            if count and received >= count:
                break


def main():
    parser = argparse.ArgumentParser(description=__doc__.splitlines()[0])
    parser.add_argument("--url", default="ws://kirch-probe.local:81/")
    parser.add_argument("--count", type=int, default=0, help="stop after N readings (0 = forever)")
    parser.add_argument("--save", help="write the last reading to this capture.json path")
    args = parser.parse_args()
    try:
        asyncio.run(listen(args.url, args.count, args.save))
    except KeyboardInterrupt:
        pass


if __name__ == "__main__":
    main()
