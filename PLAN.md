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
3. **Probe** — an ESP32 that reads the real board over Wi-Fi.
4. **Agent** — AI that uses history + probe readings to find problems and suggest fixes.
5. **3D view** — a 3D model of the circuit for every commit, with 3D diffs.

---

## Checklist

### 0 — Setup
- [x] Plan written (this file)
- [x] Data formats for circuit files decided — [`docs/formats.md`](docs/formats.md)
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

**A. Product — what the platform has** — draft 2 "circuit first" in `docs/platform.md`, waiting for both of us to agree (its section 10)
- [ ] List the pages: repo list, repo page, commits, one commit, branches, compare two commits
- [ ] What each page shows (wiring diff, firmware code diff, parts list, probe readings)
- [ ] Who sees what: public / private repos, logins in v1 or later
- [ ] What's in v1 vs later — written in `docs/platform.md`
- [ ] API shape agreed by both of us (endpoints + JSON each returns)

**D. Getting circuits in — KiCad first**
- [x] `kirch import kicad` — KiCad design → `netlist.json` + `parts.json` (tested on KiCad's own demo projects)
- [x] `kirch add` re-imports automatically when the KiCad design changed (`kirch status` says when it did)
- [x] KiCad schematic positions → `schematic.json`, so the website can draw your real schematic (checked against KiCad's own drawing)
- [ ] Engine helpers for the website: circuit summary, parts diff, ahead/behind, "can merge?" check
- [ ] Wokwi import (students, ESP32)

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

### Part 3 — Probe (Wi-Fi only)
- [~] Firmware reads 6 probe pins once a second
- [~] Wi-Fi set up from a phone over Bluetooth
- [~] Laptop finds it as `kirch-probe.local`
- [~] Sends readings over Wi-Fi and re-sends any that get lost
- [x] Laptop tools to listen to the probe, and a fake probe for testing
- [ ] Tested on the real ESP32 — **🔨 Hridhayesh** (boots + Bluetooth works; Wi-Fi test next)
- [ ] Pair the probe with a key (`kirch probe pair`)
- [ ] Update probe firmware over Wi-Fi
- [ ] Probe makes its own Wi-Fi if the local network blocks it
- [ ] Battery level + small screen
- [ ] Measure very short pulses accurately
- [ ] `kirch capture` — save probe readings into a commit
- [ ] `kirch verify` — compare the live board with the current commit
- [ ] 🎯 Done when: a commit contains real readings sent over Wi-Fi

### Part 4 — Agent
- [ ] `kirch explain` — describe a wiring change in plain English
- [ ] `kirch debug "<problem>"` — find the likely cause from history + probe
- [ ] `kirch fix` — suggest a fix as a new branch (you approve it)
- [ ] `kirch bisect --auto` — find the commit that broke a signal
- [ ] 🎯 Done when: `kirch debug` finds a real cause on a real board

### Part 5 — 3D view
- [ ] 2D breadboard editor to place parts and wires
- [ ] 3D viewer of the circuit in the browser
- [ ] 3D diff: added wires green, removed red, moved parts highlighted
- [ ] Live probe values shown on the wires
- [ ] 🎯 Done when: every commit on the website has a 3D model you can rotate

### Later
- [ ] Put it online on AWS
- [ ] Store big files (photos, binaries) in S3
- [ ] Update the circuit's own firmware over Wi-Fi
- [ ] Cloud mode for the probe (AWS IoT)
- [ ] Logins, pull requests and comments
- [ ] Import KiCad projects
- [ ] Make push/pull safe for big repos and many users
- [ ] Merge all branches into `main`

---

## Decisions (one line each)
- Name is **kirch**; repo data lives in a `.kirch` folder.
- Built from scratch in **Go**; it stores data exactly like git, so real git is our test checker.
- Circuit files: `netlist.json`, `parts.json`, `layout.json`, `capture.json` — see [`docs/formats.md`](docs/formats.md).
- Kirch's own files end in `.json` (`index.json`, `config.json`) so git can still read the folder.
- Netlists merge **net by net**: two branches changing the same net differently = conflict (changed 2026-11-14 from "per pin", which could short pins).
- Platform stack: Go server + Postgres + Next.js website; Docker locally, AWS when online.
- Push/pull uses **our own TCP protocol** (`kirch://`, TLS, port 9418), built from scratch; the website uses **HTTP** (port 8080), since browsers can't open raw TCP (changed 2026-11-14 from HTTP + JSON push/pull).
- Postgres is only an index; the repo folders on the server are the source of truth (Postgres can be rebuilt from them).
- Server uses Go's standard HTTP router and its own small migration runner (no chi / sqlc / goose); only new dependency is the `pgx` Postgres driver (2026-11-14).
- The AI never changes history by itself — it only suggests fixes on a new branch.
- The probe is Wi-Fi only; USB just for the very first flash.

## Open questions
- Which AI model to use for the agent, and the cost limit
- How to name pins for each board (`U1.GPIO9` or `U1.9`)
