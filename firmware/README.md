# Kirch probe firmware (ESP32-S3, Wi-Fi only)

The probe reads 6 signals on the circuit and sends them to the laptop over
Wi-Fi. USB is only used once, to flash the first firmware (and to read boot logs).

## What it does

1. **First boot:** no Wi-Fi saved → shows up over Bluetooth as `KIRCH_xxxxxx`.
   Open Espressif's **ESP BLE Provisioning** app (Android/iOS), pick the probe,
   enter the code `kirch1234`, then your Wi-Fi name and password.
2. **Joins Wi-Fi** and announces itself as `kirch-probe.local`.
3. **Streams readings** every second over WebSocket: `ws://kirch-probe.local:81/`.
   Readings are numbered; if the laptop misses some, it asks for them again
   (the probe keeps the last 32).

To forget the saved Wi-Fi: hold **BOOT** while powering on for 3 seconds.

## Probe pins

| Probe | GPIO | Mode | Reports |
|---|---:|---|---|
| P1 | 8 | analog | `mv`: min / max / avg |
| P2 | 3 | pulse | `edges`, `period_us`, `high_us`, `state` |
| P3 | 16 | pulse | same |
| P4 | 21 | pulse | same (servo signal) |
| P5 | 9 | analog | `mv` |
| P6 | 48 | digital | `state`, `transitions` |

Edit `include/probe_config.h` to change pins or modes.
The S3 needs about 2.5 V to read HIGH; don't divide 3.3 V signals.

## Messages

| Direction | Message |
|---|---|
| probe → laptop | `{"type":"hello", "device":{...}, "probes":{...}}` on connect |
| probe → laptop | `{"type":"capture", "seq":N, "boot_id":..., "probes":{...}}` every second |
| probe → laptop | `{"type":"gap", "from":A, "oldest":B}` when asked for frames it no longer has |
| laptop → probe | `{"type":"resend", "from":N}` |

`probes` uses the same fields as `capture.json` in `docs/formats.md`.

## Build and flash (first time, over USB)

```bash
pip install platformio
cd firmware
pio run -t upload
pio device monitor
```

## Test without the ESP32

```bash
pip install -r firmware/tools/requirements.txt
python firmware/tools/fake_probe.py --drop 0.2
python firmware/tools/probe_listen.py --url ws://localhost:8081/
```

The fake probe drops 20% of readings on purpose; the listener should ask for
them again and print every number in order.

Logic tests (pulse/analog maths and the re-send buffer), no ESP32 needed:

```bash
cd firmware
g++ -std=c++17 -Wall -Wextra -Iinclude test/test_host.cpp -o test_host
./test_host
```

## Test at home with the ESP32

1. Flash over USB, open the monitor, note the `KIRCH_xxxxxx` name.
2. Provision Wi-Fi from the phone app. Monitor should print `[wifi] connected, IP ...`
   and `[mdns] ws://kirch-probe.local:81/`.
3. Unplug USB, power from a battery or USB charger.
4. `python firmware/tools/probe_listen.py` → readings every second.
   If `kirch-probe.local` doesn't resolve, use `--url ws://<IP>:81/`.
5. Connect a known signal (e.g. a servo at 50 Hz): P4 should show
   `period 20000 us`, `high ~1500 us`.
6. Walk out of Wi-Fi range and back: the listener should print
   "missed …, asking for them again" and catch up.
7. Save one reading: `python firmware/tools/probe_listen.py --count 1 --save capture.json`.

## Not in this branch yet (next: `firmware/ota-pairing`)

- Pairing key (right now anyone on the same Wi-Fi can read the probe)
- Wireless firmware updates (OTA)
- Probe creates its own Wi-Fi when the local network blocks devices
- Battery level / low-battery warning, OLED screens
- Hardware timers for very short pulses: pulses use GPIO interrupts, so a
  10 µs pulse (HC-SR04 trigger) is counted but its width is approximate
