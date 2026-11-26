# Kirch — The Project

**Git for hardware circuits.** Plug your ESP into the Kirch module and clip on its wires, and Kirch works out your circuit, saves a version of it every time you commit, shows exactly which wires changed, and helps you find out why the board stopped working.

This file is the big picture: what we are building and how the pieces fit. The checklist lives in [`PLAN.md`](PLAN.md). Circuit files, the website plan, the probe module and how to run everything are in [`README.md`](README.md); the push/pull protocol is in [`docs/protocol.md`](docs/protocol.md).

---

## 1. The problem

When you build an ESP32 project on a breadboard, the circuit changes all the time: a wire moves, a sensor is added, a pin is swapped. Then something breaks, and nobody knows what changed.

- Code has git. Circuits have nothing. Nobody writes down "I moved the servo from GPIO9 to GPIO10".
- Drawing the circuit in a tool (KiCad, Fritzing) is slow, so makers and students skip it.
- When it breaks, you can't go back to "the version that worked", because there is no version.

## 2. The idea

**The user draws nothing, writes nothing extra, and we never touch their ESP's firmware.**

1. **Plug in.** The user plugs their ESP into our module (an ESP32-S3 with screens and clip wires) with one USB cable, and clips the wires onto their circuit.
2. **Kirch works out the circuit** from three things:
   - their code, read only (`servo.attach(9)` → a servo on GPIO9),
   - the signals our clip wires measure (50 Hz pulses on GPIO9 → that really is a servo),
   - whatever their ESP prints over serial ("distance: 23 cm" → an ultrasonic sensor).
3. **Confirm.** Kirch shows a draft: "ESP32 + servo on GPIO9 + ultrasonic sensor on GPIO3/16. Is this right?" One click to accept or fix.
4. **Commit.** `kirch commit` saves the circuit, the code and the readings together as one version.
5. **See changes.** `kirch diff` says `SERVO1.SIG: U1.GPIO9 → U1.GPIO10`, not "line 14 of a JSON file changed".
6. **Share.** `kirch push` sends it to the Kirch server; the website shows the circuit, its history, and a 3D model.
7. **Debug.** `kirch debug "servo stopped moving"` — the AI looks at the history and the readings and points at the commit and wire that broke it.

**Who it's for:** makers and students building ESP32 projects.

## 3. The demo we are building towards

1. Plug an ESP into the module, clip on the wires → Kirch shows the detected circuit → confirm.
2. `kirch commit`, move a wire, `kirch commit` again.
3. `kirch diff` → "servo moved GPIO9 → GPIO10".
4. `kirch push` → open the website → see the circuit, the commit list, and the changed wire highlighted.
5. Click a pin → every commit that ever moved it.
6. Readings → the servo signal on every commit; see exactly where it went flat.
7. Rotate the 3D model of the board; the dead wire glows red.

---

## 4. The five parts

```
 ┌───────────────────── on the bench ─────────────────────┐      ┌────── Kirch server ──────┐    ┌── browser ──┐
 │                                                        │      │                          │    │             │
 │  their ESP ──drives──► their circuit                   │      │  push/pull  :9418 (TCP)  │    │  website    │
 │     │ USB (serial output, read only)   ▲ clip wires    │      │  repo folders (truth)    │◄───│  3D view    │
 │     ▼                                  │               │      │  Postgres (index)        │HTTP│             │
 │  our module (ESP32-S3 + screens + clip wires) ◄─camera │      │  HTTP API   :8080        │    └─────────────┘
 │     │ Wi-Fi                                           │      │                          │
 │     ▼                                                  │kirch://                         │
 │  laptop: receiver (Flask) → kirch CLI ─────────────────┼─────►│                          │
 │                               ▲                        │ TLS  └──────────────────────────┘
 │                         AI agent ──► Claude API        │
 └────────────────────────────────────────────────────────┘
```

| # | Part | What it is | Status |
|---|---|---|---|
| 1 | **`kirch` command line** | Our own git, built from scratch in Go. Stores data exactly like git, plus circuit-aware diff and merge. | ✅ Done |
| 2 | **Platform** | A server (push/pull, storage, API) and a website, like GitHub but for circuits. | Server ✅ · website ⬜ |
| 3 | **Probe module** | Our ESP32-S3 board with screens and clip wires. Measures the user's circuit, reads their ESP's serial output, sends everything to the laptop over Wi-Fi. | 🔨 In progress |
| 4 | **AI agent** | `explain`, `debug`, `fix`, `bisect --auto` — uses history + readings to find causes. | ⬜ Planned |
| 5 | **3D view** | A 3D "digital twin" Kirch builds itself from the detected circuit, with live values and 3D diffs. | ⬜ Planned |

### Part 1 — `kirch` command line (done)

Works like git: `init`, `add`, `commit`, `log`, `status`, `branch`, `checkout`, `diff`, `reset`, `revert`, `merge`, `clone`, `push`, `pull`, `remote`, `config`.

What makes it different from git:
- **Wiring diff:** `kirch diff` compares which net each pin is on and reports moves in plain words.
- **Net-by-net merge:** two branches that change the same wire group differently is a conflict (never a silent short circuit).
- **Circuit files are checked and tidied** on `kirch add`, so the same circuit always gives the same bytes.
- **Real git can read a Kirch repo.** Tests compare our output with git's, byte for byte.

### Part 2 — Platform (server done, website to do)

**Server (Evin)** — one Go program, `kirch-server`:
- **Push / pull / clone** over our own protocol, `kirch://` (TCP + TLS, port 9418). See [`docs/protocol.md`](docs/protocol.md).
- **Repo folders on disk are the truth.** Every repo is a normal `.kirch` folder.
- **Postgres is only an index.** After every push, `IndexRepo()` copies commits and branches into Postgres so the website loads fast. It can be rebuilt from the folders at any time.
- **HTTP API** (port 8080) for the website: repos, branches, commits, diffs, files.
- Runs locally with `docker compose up --build`.

**Website (Hridhayesh)** — Next.js:
- Circuit page (the drawing, parts, nets), commit list with wiring summaries, one commit with changed wires highlighted, pin history ("git blame for wires"), branches + compare, readings charts, 3D view.
- Drafted in README, "Website plan"; still needs both of us to agree.

### Part 3 — Probe module (in progress)

```
their circuit + their ESP ──► our module (ESP32-S3, screens, clip wires) ──(Wi-Fi)──► laptop (Flask receiver) ──► kirch
```

- **Their ESP is completely theirs.** We never change or upload its firmware. It only plugs into our module's USB port.
- **Pin data comes from our clip wires** — a chip can't report its own pins without our code on it. 6 clip wires today; multiplexers later so one module can watch many more pins.
- **Their ESP's serial output** (whatever their program prints) comes over the USB cable, and the module forwards it.
- **Screens** on the module show status, Wi-Fi and live readings; battery level too.
- **Camera (optional):** sees where parts sit and which way they face, for the 3D model.
- **Laptop side:** a Flask receiver gets one combined stream (clip-wire readings + serial output + camera) over Wi-Fi; finds the module as `kirch-probe.local`; asks again for readings it missed.
- `kirch capture` saves readings into a commit; `kirch verify` compares the live board with the current commit.

Built so far: the 6-clip-wire firmware, Wi-Fi setup from a phone over Bluetooth, mDNS, re-sending lost readings, and laptop tools with a fake probe. Tested on the real board: boots and Bluetooth work; Wi-Fi test next. The USB serial link, screens and the Flask receiver are not started.

### Part 4 — AI agent (planned)

- `kirch explain` — describe a wiring change in plain English.
- `kirch debug "<problem>"` — find the likely cause from history + readings.
- `kirch fix` — suggest a fix **as a new branch** you approve. The AI never rewrites history itself.
- `kirch bisect --auto` — find the commit that broke a signal, using readings on each commit.

### Part 5 — 3D view (planned)

- **Auto-layout:** Kirch places the detected parts on a virtual breadboard and routes tidy wires (`layout.json`).
- **Real 3D models** for common parts (ESP32, servo, HC-SR04, LEDs) from a free 3D part library; unknown parts are a labelled box the user can name.
- **With a camera:** parts placed where they really are, facing the same way. Wires are always drawn by Kirch.
- **Live values:** wires glow green when a signal flows, red when a pin is dead.
- **3D diff:** added wires green, removed red, moved parts highlighted.

---

## 5. What a commit holds

| File | Answers | Written by |
|---|---|---|
| `parts.json` | Which parts are on the board? | Kirch (detected), confirmed by the user |
| `netlist.json` | Which pins are connected to each other? | Kirch (detected), confirmed by the user |
| `layout.json` | Where is each part and wire, physically? | Kirch auto-layout (+ camera) |
| `capture.json` | What did the module measure? | `kirch capture` |
| `firmware/` | The user's ESP code | the user (Kirch only reads it) |

Pins are written `PART.PIN`: pins on the user's ESP by GPIO (`U1.GPIO9`), other parts' pins by name (`SERVO1.SIG`). Details: README, "Circuit files".

## 6. How a change travels

1. The user moves a wire; the clip wires see new signals, Kirch updates the draft circuit, the user confirms.
2. `kirch commit` saves circuit files + code + readings in `.kirch/`, byte-identical to git.
3. `kirch push` opens a TLS connection on `kirch://`, sends only the missing objects; the server writes them into the repo folder.
4. The server runs `IndexRepo()` → new commits and branches go into Postgres.
5. The website calls the HTTP API: lists come from Postgres, diffs and files from the repo folder.

## 7. Connections

| From → to | How | Port |
|---|---|---|
| Their circuit → our module | Clip wires (pin data) | — |
| Their ESP → our module | USB cable (their serial output, read only) | — |
| Module → laptop | WebSocket over Wi-Fi | 81 |
| Laptop finds module | mDNS (`kirch-probe.local`) | 5353 |
| `kirch push` / `pull` / `clone` → server | Custom TCP + TLS (`kirch://`) | 9418 |
| Website → server | HTTP + JSON | 8080 |
| Server → Postgres | Postgres protocol (`pgx`) | 5432 |

Our own TCP protocol only where we control both ends (CLI ↔ server); HTTP wherever a browser is involved.

## 8. Tech stack

| Piece | Tech |
|---|---|
| CLI, server, engine | Go (one shared engine in `internal/`) |
| Database | Postgres 16 (index only) |
| Website | Next.js |
| 3D | Three.js |
| Module firmware | C++ on ESP32-S3 (PlatformIO) |
| Laptop receiver | Python (Flask) |
| AI | Claude API |
| Run locally | Docker Compose |
| Online (later) | AWS; big files in S3 |

## 9. Rules we keep

- **One engine.** The CLI, the server and the agent call the same Go code in `internal/`.
- **Repo folders are the truth.** Postgres can always be rebuilt from them.
- **Real git checks us.** Same ids and output as git, byte for byte.
- **The user draws nothing.** Kirch detects; the user only confirms.
- **We never touch their ESP's firmware.** We read their code and their serial output; we measure their pins with our own wires.
- **The AI only suggests.** Fixes come as a branch the user approves.

---

## 10. Where we are (25 Nov 2026)

| Part | Status | Owner |
|---|---|---|
| 0. Setup — plan, formats, CI, v0.1.0 release | ✅ Done | Hridhayesh |
| 1. `kirch` command line | ✅ Done | Hridhayesh |
| 2. Server — push/pull, Postgres, indexer, HTTP API, Docker | ✅ Done (branch `server-backend/Evin`) | Evin |
| 2. Website spec + pages | 🔨 Draft spec | Hridhayesh |
| 2. Circuit detection from code + measurements + serial output | ⬜ Planned | Hridhayesh |
| 3. Probe module — clip wires, USB serial, screens, Flask receiver | 🔨 Clip-wire firmware built; Wi-Fi test next | Hridhayesh |
| 4. AI agent | ⬜ Planned | Open |
| 5. 3D digital twin | ⬜ Planned | Open |

## 11. What's next

1. **Agree the website** — pages and API calls (README, "Website plan").
2. **Server:** GitHub login, tokens for `kirch push`, who may push to a repo, new-repo call; then circuit-aware API calls (circuit at a commit, pin history, ahead/behind, compare + can-merge, readings).
3. **Module:** Wi-Fi test on the real board, USB serial from their ESP, screens, Flask receiver.
4. **Detection:** code + clip-wire signals + serial output → draft `parts.json` + `netlist.json` → "is this right?".
5. **Website pages,** on fake data until the API is ready.
6. Then the AI agent and the 3D view.

## 12. Open questions

- Which USB chips on their boards we support first (CH340, CP2102, native USB).
- Which screens on the module (size, how many).
- Multiplexer chip and how many pins (later).
- Which camera: an ESP32-S3 camera board on the module, or the phone.
- Big loads (servos, motors) need more power than USB gives — how we tell the user to add a supply.
- Which AI model for the agent, and the cost limit.
- How serial output is stored in a commit (today `capture.json` covers clip-wire readings only).

## 13. Words we use

| Word | Meaning |
|---|---|
| **Net** | A group of pins all wired together (e.g. `SERVO_SIG` = `SERVO1.SIG` + `U1.GPIO9`). |
| **Netlist** | The list of all nets — "what's connected to what". |
| **Wiring diff** | Which pins moved between nets from one commit to the next. |
| **Module / probe** | Our ESP32-S3 board with screens and clip wires. Same thing. |
| **Clip wires** | The module's wires clipped onto the user's circuit; they measure the pins. |
| **Serial output** | Whatever the user's program prints (`Serial.print`), read over USB. |
| **Agent** | The AI helper: `kirch debug` / `explain` / `fix` / `bisect` (Part 4). |
| **Capture** | One set of readings saved into a commit. |
| **Digital twin** | The 3D model of the circuit that Kirch builds itself. |
| **Indexer** | The server step that copies new commits into Postgres after a push. |
