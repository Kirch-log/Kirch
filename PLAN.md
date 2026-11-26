# Kirch — Plan

**Git for hardware circuits:** save versions of a circuit, see what changed, go back, and debug the real board with a probe + AI.

**How to use this file**
- One checklist for both of us — whoever finishes an item ticks it.
- `[x]` done · `[~]` built, not tested yet · `[ ]` to do
- **🔨 Name** = that person is working on it right now (add your name before you start).
- Code lives on branches; everything merges into `main` at the end (this plan is kept in sync on `main`).

---

## The 5 Parts

1. **`kirch` command line** — our own git for circuits (commit, branch, diff, merge, push).
2. **Platform** — a GitHub-like server + website to share circuits.
3. **Probe** — our module (ESP32-S3 + screens + clip wires; multiplexers later): measures the user's circuit, gets their ESP's serial output over USB, and forwards everything to us over Wi-Fi. **We never touch their ESP's firmware.**
4. **Agent** — AI that uses history + probe readings to find problems and suggest fixes.
5. **3D view** — a 3D "digital twin" of the circuit for every commit, built by Kirch itself (no drawing needed), with 3D diffs.

**The user does almost nothing:** plug their ESP into our module and clip on the wires (optionally point a camera at the bench). Kirch works out the circuit from their code (read only) + the measurements, and they just confirm "is this right?".

---

## Checklist

### 0 — Setup
- [x] Plan written (this file)
- [x] Data formats for circuit files decided — README, "Circuit files"
- [x] Tech stack + hosting picked
- [x] Repo set up with CI (every push builds and tests everything)
- [x] CD: pushing a version tag publishes `kirch` for Windows/Mac/Linux + probe firmware (v0.1.0 out)

### Part 1 — `kirch` command line ✅
- [x] 1. Store files the same way git does (real git can read them)
- [x] 2. `kirch init` — make a new repo
- [x] 3. `kirch add` — stage files; circuit files are checked and tidied
- [x] 4. `kirch commit` — save a version
- [x] 5. `kirch log` — see history
- [x] 6. `kirch status` — see what changed
- [x] 7. `kirch branch` / `checkout` — make and switch branches
- [x] 8. `kirch diff` — shows wiring changes like "servo moved GPIO9 → GPIO10"
- [x] 9. `kirch reset` / `revert` — undo changes or old commits
- [x] 10. `kirch merge` — combine branches; netlists merge net by net
- [x] 11. `kirch clone` / `push` / `pull` — share through a folder
- [x] 12. Fixes after testing: `HEAD~1`, smarter revert, `origin/` in log, `.kirchignore`

### Part 2 — Platform
Three tracks. **A** comes first (small) — it decides what the API must return. Push/pull server doesn't wait on A.

**A. Product — what the platform has** — draft "circuit first" in the README ("Website plan"), waiting for both of us to agree
- [ ] List the pages: repo list, repo page, commits, one commit, branches, compare two commits
- [ ] What each page shows (wiring diff, firmware code diff, parts list, probe readings)
- [ ] Who sees what: public / private repos, logins in v1 or later
- [ ] What's in v1 vs later — written in the README
- [ ] API shape agreed by both of us (endpoints + JSON each returns)

**D. Getting circuits in — auto-detect first, the user writes nothing**
- [ ] Read the user's ESP code (never change it) → which pins are used and for what (`servo.attach(9)` → servo on GPIO9)
- [ ] Recognise parts from the signals our clip wires measure (50 Hz pulses = servo, trigger + echo = ultrasonic sensor, …)
- [ ] Use their ESP's serial output too ("distance: 23 cm" → an ultrasonic sensor)
- [ ] Combine code + signals into a draft circuit → `parts.json` + `netlist.json`
- [ ] "Is this right?" — the user confirms or fixes the draft with one click
- [ ] Engine helpers for the website: circuit summary, parts diff, ahead/behind, "can merge?" check

Parked (not part of the product — users don't draw circuits): a KiCad import was built before this decision (`kirch import kicad`); it stays in the code but gets no more work. Wokwi import dropped.

**B. Backend — the main server** — **Evin** ✅ (branch `server-backend/Evin`)
- [x] Protocol spec for push/pull — [`docs/protocol.md`](docs/protocol.md) (custom TCP + TLS, `kirch://`, port 9418)
- [x] TCP push/pull server: HELLO, REFS, WANT/HAVE, OBJ, DONE, UPDATE, OK/ERR
- [x] `kirch push` / `pull` / `clone` speak `kirch://` (engine talks to folders and servers through one interface)
- [x] Database (Postgres) that records repos and commits
- [x] Indexer: `IndexRepo()` copies commits + branches into Postgres after each push (and for every repo at start)
- [x] HTTP API the website reads from (repos, commits, diffs) — port 8080
- [x] Run everything locally with one command (`docker compose up --build`)
- [x] Tests against a real Postgres (CI too) + end-to-end script `scripts/e2e-server.sh`

**C. Frontend — the website**
- [ ] Pages from track A
- [ ] Wiring diff view + firmware code diff
- [ ] Connected to the API (fake data until the API is ready)

- [ ] 🎯 Done when: one person pushes, another pulls, both see the commit on the website

### Part 3 — Probe (our module)
How it connects: **their circuit + their ESP → our module (ESP32-S3, screens, clip wires) → Wi-Fi → laptop (Flask server) → Kirch.**
- **Their ESP is completely theirs:** we never change or upload its firmware. It only plugs into our module.
- Without our code on their chip it can't report its pins, so **pin data comes from our clip wires** (multiplexers later for many more pins); their ESP gives us its serial output.

**A. Module basics (built)**
- [~] Wi-Fi set up from a phone over Bluetooth
- [~] Laptop finds it as `kirch-probe.local`
- [~] Sends readings over Wi-Fi and re-sends any that get lost
- [x] Laptop tools to listen to the probe, and a fake probe for testing
- [ ] Tested on the real ESP32 — **🔨 Hridhayesh** (boots + Bluetooth works; Wi-Fi test next)

**B. Clip wires — the pin data (main source)**
- [~] Reads 6 pins once a second (analog / on-off / pulses)
- [ ] Measure very short pulses accurately
- [ ] Later: multiplexers so one module can watch many more pins

**C. Their ESP plugged into our module (read only)**
- [ ] USB link: their ESP plugs into our module's "USB" port → we get whatever their program prints (`Serial.print`) and forward it
- [ ] Check our board can power theirs through that port (5 V out jumper); big loads need their own supply
- [ ] Common USB chips on their boards (CH340, CP2102, native USB)
- [ ] Tap wire: listen to their board's serial line with one wire, nothing changed on their side (first test: the FPGA accelerator's ESP bridge) — **🔨 Hridhayesh**

**D. Our module's screens**
- [ ] Screens on the module: status, Wi-Fi, live readings (2 × SSD1306) — **🔨 Hridhayesh**
- [ ] Battery level

**E. Camera (optional)**
- [ ] Camera above the bench (ESP32-S3 camera board or phone) → which parts are where, and which way they face

**F. Getting the data to us**
- [ ] Laptop receiver (Flask server) gets the data over Wi-Fi
- [ ] One combined stream: clip-wire readings + their serial output + camera
- [ ] `kirch capture` — save readings into a commit
- [ ] `kirch verify` — compare the live board with the current commit
- [ ] Pair the module with a key (`kirch probe pair`)
- [ ] Update our module's firmware over Wi-Fi (ours only, never theirs)
- [ ] Module makes its own Wi-Fi if the local network blocks it
- [ ] 🎯 Done when: plug in their ESP + clip on the wires → Kirch knows the circuit, and a commit contains its real readings

### Part 4 — Agent
- [ ] `kirch explain` — describe a wiring change in plain English
- [ ] `kirch debug "<problem>"` — find the likely cause from history + probe
- [ ] `kirch fix` — suggest a fix as a new branch (you approve it)
- [ ] `kirch bisect --auto` — find the commit that broke a signal
- [ ] 🎯 Done when: `kirch debug` finds a real cause on a real board

### Part 5 — 3D view (a digital twin Kirch builds itself)
- [ ] Auto-layout: place the detected parts on a virtual breadboard and route neat wires → `layout.json`
- [ ] 3D models of real parts (ESP32, servo, ultrasonic sensor, LEDs…) from a free 3D part library
- [ ] 3D viewer of the circuit in the browser (rotate, zoom)
- [ ] With a camera: parts placed where they really are and facing the same way (wires still drawn by Kirch)
- [ ] Unknown parts shown as a labelled box; the user can add missed parts (e.g. a resistor) with one click
- [ ] Live values: wires glow green when a signal flows, red when a pin is dead; servo angle and sensor distance shown
- [ ] 3D diff: added wires green, removed red, moved parts highlighted
- [ ] 🎯 Done when: plugging in an ESP gives a 3D model you can rotate, with live values, on every commit

### Later
- [ ] Put it online on AWS
- [ ] Store big files (photos, binaries) in S3
- [ ] Cloud mode for the probe (AWS IoT)
- [ ] Pull requests and comments (GitHub login + tokens are v1, see README "Website plan")
- [ ] Make push/pull safe for big repos and many users
- [ ] Merge all branches into `main`

---

## Decisions (one line each)
- Name is **kirch**; repo data lives in a `.kirch` folder.
- Built from scratch in **Go**; it stores data exactly like git, so real git is our test checker.
- Circuit files: `netlist.json`, `parts.json`, `layout.json`, `capture.json` — see the README, "Circuit files".
- Kirch's own files end in `.json` (`index.json`, `config.json`) so git can still read the folder.
- Netlists merge **net by net**: two branches changing the same net differently = conflict (changed 2026-11-14 from "per pin", which could short pins).
- Platform stack: Go server + Postgres + Next.js website; Docker locally, AWS when online.
- Push/pull uses **our own TCP protocol** (`kirch://`, TLS, port 9418), built from scratch; the website uses **HTTP** (port 8080), since browsers can't open raw TCP (changed 2026-11-14 from HTTP + JSON push/pull).
- Postgres is only an index; the repo folders on the server are the source of truth (Postgres can be rebuilt from them).
- Server uses Go's standard HTTP router and its own small migration runner (no chi / sqlc / goose); only new dependency is the `pgx` Postgres driver (2026-11-14).
- The AI never changes history by itself — it only suggests fixes on a new branch.
- **The user writes nothing (2026-11-25).** Kirch works out the circuit from their ESP code (read only) + our measurements; the user only confirms. **No KiCad or other drawing tools** — the KiCad import already built is parked.
- **We never touch their ESP's firmware (2026-11-25).** No agent, no uploads. Their ESP plugs into our module by USB and we take whatever it prints.
- **Pin data comes from our clip wires** (6 now, multiplexers later for more) — a chip can't report its pins without code on it.
- Our module = ESP32-S3 + screens + clip wires (+ multiplexers later); it forwards everything to the laptop over Wi-Fi (Flask server).
- **3D is a digital twin Kirch builds itself** — not a copy of their desk. A camera (optional) adds where parts really are and which way they face; wires are always drawn by Kirch.
- Pins on the user's ESP are named by GPIO (`U1.GPIO9`); parts' pins by their name (`SERVO1.SIG`).

## Open questions
- Which AI model to use for the agent, and the cost limit
- Which camera: ESP32-S3 camera board on the probe, or the phone
- Which USB chips on their boards we support first (CH340, CP2102, native USB)
- Big loads (servos, motors) need more power than USB gives — tell the user to add a supply
- Multiplexer chip and how many pins (later)
- Which screens on the module (size, how many)
