# Kirch — Data Formats (draft v1)

What a Kirch commit stores for a circuit. Closes the open question
"Exact `netlist.json` format" (blocks step 8 wiring diff and step 10 merge).

| File | Answers | Used by |
|---|---|---|
| `parts.json` | What parts are on the board? | diff, 3D, agent |
| `netlist.json` | Which pins are electrically connected? | wiring diff, merge, verify, agent |
| `layout.json` | Where is each part and wire physically? | 3D view, rewiring steps |
| `schematic.json` | Where is everything drawn on the schematic? (from KiCad) | website drawing |
| `capture.json` | What did the probe measure? | verify, bisect, agent |
| `firmware/` | Code running on the circuit's board | normal text diff |

Every file starts with a `format` field (`kirch.<name>/<version>`) so we can
change formats later without breaking old commits.

---

## Rules for all files (so diffs stay clean)

1. **Stable IDs.** Parts have short IDs (`U1`, `R2`, `SERVO1`) that never change
   when a part is moved or edited. Diff and merge match on IDs, not positions.
2. **A pin is written `PART.PIN`**, e.g. `U1.GPIO9`, `SERVO1.SIG`.
3. **Canonical form.** `kirch add` rewrites each file before hashing:
   keys sorted, lists sorted, 2-space indent, one item per line, trailing newline.
   Same circuit → same bytes → same hash, and a one-wire change is a one-line diff.
4. **Objects keyed by ID, not arrays**, wherever order doesn't matter
   (two people adding different parts then merge without conflict).

---

## `parts.json`

```json
{
  "format": "kirch.parts/1",
  "parts": {
    "R1": {
      "type": "resistor",
      "value": "1k"
    },
    "SERVO1": {
      "type": "servo",
      "model": "SG90",
      "pins": ["GND", "SIG", "VCC"]
    }
  }
}
```

| Field | Required | Meaning |
|---|---|---|
| `type` | yes | Generic kind: `mcu`, `resistor`, `capacitor`, `led`, `sensor`, `servo`, `power`, `connector`, … |
| `model` | no | Exact part, e.g. `ESP32-S3-DevKitC-1`, `HC-SR04` — also picks the 3D model |
| `value` | no | `1k`, `100nF`, `5V` |
| `pins` | no | Pin names, or pin numbers for KiCad imports. Optional for 2-pin passives (`1`, `2` assumed) |
| `pin_names` | no | Pin number → readable name, e.g. `{"9": "GPIO9"}` (KiCad imports) |
| `symbol` | no | KiCad library symbol, e.g. `Device:R` (KiCad imports) |
| `footprint` | no | KiCad footprint, e.g. `Resistor_SMD:R_0603_1608Metric` (KiCad imports) |
| `description` | no | Symbol description from the library |
| `notes` | no | Free text |

Top level may also have `"title"` (the schematic title).

**From KiCad** (`kirch import kicad`): pins are named by **number** (`U1.9`), because pin
names repeat (several `GND` pins on one chip). The readable names go in `pin_names`.
`type` is guessed from the reference (`R1` → resistor, `U1` → ic, ESP32 / ATmega / RP2040… → mcu).

---

## `netlist.json`

A **net** is a group of pins that are all connected together.

```json
{
  "format": "kirch.netlist/1",
  "nets": {
    "GND": ["SERVO1.GND", "U1.GND", "US1.GND"],
    "SERVO_SIG": ["SERVO1.SIG", "U1.GPIO9"]
  }
}
```

- Net names are free text; `GND`, `5V`, `3V3` are conventional.
- Each pin appears in **at most one** net (checked by `kirch add`).
- Pins not in any net are unconnected.
- Optional `"source"` says where it came from, e.g. `"kicad:servo-board.kicad_sch"`.
  KiCad imports leave out nets with a single pin (unconnected pins) and keep KiCad's net
  names (`/SERVO/SIG`, `Net-(R1-Pad1)`).

**How the wiring diff reads it** (step 8): compare each pin's net in old vs new.
`SERVO1.SIG: GPIO4 → GPIO9` is reported as
"servo signal moved from GPIO4 to GPIO9", not as JSON line changes.

**Merge** (step 10): nets merge **net by net** (changed 2026-11-14, was per pin).

- A net changed on one branch only → that branch's pins.
- A net both branches changed the same way → that change.
- A net both branches changed differently → **conflict**. Example: both swap
  `U1.GPIO9` for a different pin. A per-pin merge would wire GPIO10 *and*
  GPIO11 to the servo — a short nobody made.
- A pin each branch moved to a different net → **conflict** (one net only per pin).

On a conflict, `netlist.json` keeps our version of the net and lists both
versions under a `"conflicts"` key:

```json
"conflicts": {
  "nets": { "SIG": { "ours": ["SERVO1.SIG", "U1.GPIO11"], "theirs": ["SERVO1.SIG", "U1.GPIO10"] } },
  "pins": { "U1.GPIO1": { "ours": "B_NET", "theirs": "C_NET" } }
}
```

`kirch add` refuses the file until `"conflicts"` is fixed and deleted.

---

## `schematic.json`

The schematic drawing, written by `kirch import kicad` (and kept up to date by
`kirch add`). The website draws it exactly where it was drawn in KiCad.
Netlist = what's connected; schematic = where it's drawn.

```json
{
  "format": "kirch.schematic/1",
  "source": "kicad:hw/servo-board.kicad_sch",
  "units": "mm",
  "main": "servo-board.kicad_sch",
  "sheets": {
    "servo-board.kicad_sch": {
      "parts": [
        { "ref": "R1", "unit": 1, "value": "10k", "at": [157.48, 85.09], "rotation": 180,
          "box": [156.464, 81.28, 158.496, 88.9],
          "pins": { "1": { "at": [157.48, 88.9], "end": [157.48, 87.63] } },
          "shapes": [ { "kind": "poly", "points": [[156.464, 82.55], [158.496, 82.55]] } ] }
      ],
      "wires": [[185.42, 76.2, 198.12, 76.2]],
      "junctions": [[76.2, 50.8]],
      "power": [{ "name": "GND", "at": [86.36, 63.5], "rotation": 0 }],
      "labels": [{ "text": "SERVO_SIG", "kind": "label", "at": [120, 40], "rotation": 0 }],
      "no_connects": [], "sheets": []
    }
  }
}
```

- Coordinates in **millimetres**, x to the right, **y down** (KiCad's page coordinates).
- `parts` is a list: a chip with several units (two halves of a tube, four gates) appears once per unit.
- `pins`: `at` = where wires connect, `end` = where the pin meets the body; keys are pin numbers (same as `netlist.json`).
- `shapes` are the symbol's lines already placed on the page: `poly` (closed when first = last point), `circle` (`[centre, [radius, 0]]`), `arc` (start, middle, end).
- `power` = GND / +5V symbols, `labels` = net labels, `sheets` = boxes for sub-sheets (each sub-sheet file is another entry in `sheets`).
- Checked against KiCad's own demos: pin tips land on wire ends (100% on the ECC83 and PIC programmer demos; the rest are unused chip pins).

---

## `layout.json`

Physical placement — what the 3D view draws and what rewiring steps describe.
The netlist is the truth for *connections*; layout is the truth for *positions*.

```json
{
  "format": "kirch.layout/1",
  "board": { "type": "breadboard-830" },
  "placements": {
    "SERVO1": { "offboard": true },
    "U1": { "at": "A3", "rotation": 0 }
  },
  "wires": {
    "W1": { "from": "J10", "to": "+5", "color": "red", "kind": "jumper" }
  }
}
```

- **Holes** use breadboard names: rows `A`–`J`, columns `1`–`63`, rails `+1..`/`-1..`
  (or `+`/`-` with a column, e.g. `+5`).
- `at` = hole of the part's pin 1. `rotation` = 0/90/180/270.
- `offboard: true` for parts hanging off the board on wires (servos, sensors on leads).
- `wires` keyed by ID so adding one wire doesn't renumber the rest.
- `kind`: `jumper` (flexible), `rigid`, `short`. Rigid/short wires and crossings
  are where a **photo** is the fallback — the 3D view can't show those faithfully.
- **Check:** connections implied by the layout must match the netlist.
  A mismatch is a warning on commit.

---

## `capture.json`

One probe reading, attached to the commit by `kirch capture`. The probe sends
the same JSON over Wi-Fi; Kirch adds which net each probe clip was on.

```json
{
  "format": "kirch.capture/1",
  "device": { "id": "kirch-probe-3fa2", "firmware": "0.1.0" },
  "captured_at": "2026-11-13T14:02:11Z",
  "window_ms": 1000,
  "probes": {
    "P1": { "net": "5V", "mode": "analog", "mv": { "min": 4920, "max": 5010, "avg": 4975 } },
    "P2": { "net": "US_TRIG", "mode": "pulse", "edges": 20, "period_us": 100000, "high_us": 10 },
    "P4": { "net": "SERVO_SIG", "mode": "pulse", "edges": 100, "period_us": 20000, "high_us": 1500 }
  }
}
```

| Mode | Fields |
|---|---|
| `analog` | `mv`: `min`, `max`, `avg` (millivolts at the probe pin) |
| `digital` | `state` (0/1), `transitions` |
| `pulse` | `edges`, `period_us`, `high_us` (median over the window), `state` (level at window end) |
| any | `dropped_edges` if the probe saw more edges than it could store (reading is partial) |

- `net` links a probe to the netlist, so `kirch verify` can say
  "SERVO_SIG pulse width changed 1500 → 0 µs" instead of "P4 changed".
- Probes not clipped to anything are left out.
- Raw sample arrays (long recordings) go to big-file storage later, not here.

---

## Full example — ultrasonic + servo on ESP32-S3

**parts.json**
```json
{
  "format": "kirch.parts/1",
  "parts": {
    "PWR1": { "type": "power", "model": "USB-5V", "pins": ["5V", "GND"] },
    "R1": { "type": "resistor", "value": "1k" },
    "R2": { "type": "resistor", "value": "2k" },
    "SERVO1": { "type": "servo", "model": "SG90", "pins": ["GND", "SIG", "VCC"] },
    "U1": { "type": "mcu", "model": "ESP32-S3-DevKitC-1" },
    "US1": { "type": "sensor", "model": "HC-SR04", "pins": ["ECHO", "GND", "TRIG", "VCC"] }
  }
}
```

**netlist.json**
```json
{
  "format": "kirch.netlist/1",
  "nets": {
    "5V": ["PWR1.5V", "SERVO1.VCC", "U1.5V", "US1.VCC"],
    "ECHO_5V": ["R1.1", "US1.ECHO"],
    "ECHO_3V3": ["R1.2", "R2.1", "U1.GPIO16"],
    "GND": ["PWR1.GND", "R2.2", "SERVO1.GND", "U1.GND", "US1.GND"],
    "SERVO_SIG": ["SERVO1.SIG", "U1.GPIO9"],
    "US_TRIG": ["U1.GPIO3", "US1.TRIG"]
  }
}
```

(R1/R2 divide the 5 V ECHO down to ~3.3 V for the ESP32.)

**Change in the next commit:** servo signal moved to GPIO10.
```diff
-    "SERVO_SIG": ["SERVO1.SIG", "U1.GPIO9"],
+    "SERVO_SIG": ["SERVO1.SIG", "U1.GPIO10"],
```
`kirch diff` shows: **SERVO1.SIG: U1.GPIO9 → U1.GPIO10**

---

## Open questions

- Pin names per board model (`U1.GPIO9` vs `U1.9`): use the board's silkscreen,
  kept in a small parts library?
- Non-breadboard layouts (perfboard, PCB) — `board.type` leaves room, v1 is breadboard only.
- Capture with several windows (before / after a change) — one file per capture, or a list?
