# Trance — Plan

**Git for hardware circuits.** Version your circuit, compare versions, go back, and debug the real board with a probe + AI agent.

Personal project. Core pieces built **from scratch**.

---

## The 5 Parts

| # | Part | Role | Goal |
|---|---|---|---|
| 1 | **`trance` CLI** | Memory | Own git: commit, branch, diff, checkout, revert, merge, push |
| 2 | **Platform** | Sharing | GitHub-like server + website |
| 3 | **Probe** | Eyes | ESP32-S3 reads real board |
| 4 | **Agent** | Brain | Uses history + probe to find problems and suggest fixes |
| 5 | **3D View** | Face | 3D model of the circuit for every commit, with 3D diff |

---

## Decisions Made

- **Name:** `trance`, data in `.trance/`
- **Git engine:** from scratch (no go-git)
- **Language:** Go
- **Cobra:** command parsing only — all logic ours
- **Engine = clean Go functions** (`Commit()`, `Diff()`, `Log()`) so CLI *and* agent can call them
- **Agent never changes history alone** — suggests fixes as branches, user confirms
- Old ReWeird code ignored — fresh start

---

## Storage (git model)

```
.trance/
├── HEAD           → current branch
├── index          → staging area
├── refs/heads/    → branches (file = commit hash)
└── objects/       → blobs, trees, commits (SHA-named, zlib)
```

**A commit stores:** `netlist.json`, `parts.json`, `layout.json`, `firmware/`, later `capture.json` (probe readings).

---

## Part 1 — `trance` CLI (NOW)

**Layout**
```
trance/
├── main.go
├── cmd/        ← Cobra commands (thin)
└── internal/   ← engine: repo, objects, index, refs, diff, merge
```

**Steps**
1. Object store — hash, compress, save, read ← **next**
2. `init`
3. `add` + index
4. `commit`
5. `log` 🎯 *working history tool*
6. `status`
7. `branch` / `checkout`
8. `diff` + **wiring diff** for netlist 🎯
9. `reset` / `revert`
10. `merge`
11. `push` / `pull` / `clone` to local folder 🎯 *Part 1 done*

**Per step:** engine code → test → Cobra command → try it → compare with real git.

---

## Part 2 — Platform
- Go server, push/pull over HTTP (own simple protocol)
- Postgres: users, repos, commit summaries
- Next.js + TS + Tailwind site: repos, commits, diff view
- Run locally in Docker first

## Part 3 — Probe (Wi-Fi only, no USB)
- ESP32-S3, JSON readings, battery powered
- Wi-Fi setup over Bluetooth from a phone (first time only)
- Found on the network as `trance-probe.local` (mDNS)
- Streams readings over WebSocket; numbered so dropped ones are re-sent
- `trance probe pair` → pair once with a shared key
- `trance capture` → `capture.json`, attach to commits
- Fallback: probe creates its own Wi-Fi network when the local one blocks devices
- Probe firmware updates over Wi-Fi (OTA) — USB is only used once, to flash the very first firmware

## Part 4 — Agent
- `trance debug "<problem>"` — reads commits + probe, narrows cause, suggests fix
- `trance fix` — proposes fix as new branch
- `trance explain` — wiring diff in plain English
- `trance bisect --auto` — finds commit that broke a signal
- LLM (Claude) + tool use; tools = our engine + probe

---

## Part 5 — 3D View
- Main way to see a commit on the website
- Built in the browser (React Three Fiber / Three.js) from `netlist.json` + `layout.json` — no 3D file stored in commits
- Part models in glTF (converted from KiCad's free 3D library)
- 3D diff: added wires green, removed red, moved parts highlighted
- Live probe values shown on the wires (green/red per connection)
- 2D breadboard editor to enter the layout
- Photo only as last resort, for what 3D can't show (crossed, short or rigid wires)

## Later (plan after Part 5)

- OTA firmware updates for the circuit board
- Cloud mode (AWS IoT Core)
- Deployment / hosting (AWS or simpler)
- Big files (photos, binaries) → S3-style storage
- Pull requests, comments, auth
- KiCad import
- Network protocol hardening (auth, resume, large repos)

## Open Questions
- Exact `netlist.json` format
- Merge strategy for netlists (conflicts in wiring?)
- Which LLM model / cost limits for agent
- Hosting choice
