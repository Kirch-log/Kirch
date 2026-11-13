# Kirch — Plan

**Git for hardware circuits.** Version your circuit, compare versions, go back, and debug the real board with a probe + AI agent.

Personal project. Core pieces built **from scratch**.

**Status: plan locked 2026-11-12.** Changes from here = new decision entry.

---

## The 5 Parts

| # | Part | Role | Goal |
|---|---|---|---|
| 1 | **`kirch` CLI** | Memory | Own git: commit, branch, diff, checkout, revert, merge, push |
| 2 | **Platform** | Sharing | GitHub-like server + website |
| 3 | **Probe** | Eyes | ESP32-S3 reads real board |
| 4 | **Agent** | Brain | Uses history + probe to find problems and suggest fixes |
| 5 | **3D View** | Face | 3D model of the circuit for every commit, with 3D diff |

---

## Decisions Made

- **Name:** `kirch`, data in `.kirch/`
- **Git engine:** from scratch (no go-git)
- **Language:** Go
- **Go module:** `github.com/Kirch-log/kirch`
- **Cobra:** command parsing only — all logic ours
- **Engine = clean Go functions** (`Commit()`, `Diff()`, `Log()`) so CLI *and* agent can call them
- **Object format:** git-identical (`<type> <size>\0<content>`, zlib, same tree/commit layout) → hashes match `git hash-object`, real git = test oracle
- **Hash:** SHA-1 (git default)
- **Index:** simple JSON (path → hash, mode, mtime, size) — not git's binary DIRC
- **Agent never changes history alone** — suggests fixes as branches, user confirms
- Old ReWeird code ignored — fresh start

## Decision Log

| Date | Decision |
|---|---|
| 2026-11-13 | **Data formats** set in [`docs/formats.md`](docs/formats.md): `parts.json`, `netlist.json`, `layout.json`, `capture.json`. Netlist merge v1 = per pin, conflict only when both sides move the same pin. |
| 2026-11-13 | **Part 2 stack + hosting** chosen (see Part 2). Local Docker first, AWS when deployed. |

---

## Storage (git model)

```
.kirch/
├── HEAD           → current branch
├── index          → staging area (JSON)
├── refs/heads/    → branches (file = commit hash)
└── objects/       → blobs, trees, commits (SHA-1 named, zlib)
```

**A commit stores:** `netlist.json`, `parts.json`, `layout.json`, `firmware/`, later `capture.json` (probe readings).

---

## Part 1 — `kirch` CLI (NOW)

**Layout**
```
kirch/
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

**Step 1 detail — `internal/objects`**
- `HashObject(type, data) → sha` — header + SHA-1 + zlib, write `.kirch/objects/ab/cdef…`
- `ReadObject(sha) → (type, data)` — decompress, parse header, check size
- Tests: our hash == `git hash-object` for same content
- Blobs first; trees + commits come with `commit` step

---

## Part 2 — Platform
- Go server, push/pull over HTTP (own simple protocol)
- Postgres: users, repos, commit summaries
- Next.js + TS + Tailwind site: repos, commits, diff view
- Run locally in Docker first

**Stack**

| Piece | Choice |
|---|---|
| Server | Go `net/http` + `chi` router |
| Repo storage | `.kirch` repos on the server's disk (shares the Part 1 engine) |
| Database | Postgres 16 · `pgx` driver · `sqlc` (SQL → Go) · `goose` migrations |
| Push / pull | HTTP + JSON: client and server compare hashes, missing objects sent as one gzip bundle; branch moves only if nobody pushed first |
| Website | Next.js (App Router) + TypeScript + Tailwind + shadcn/ui + TanStack Query |
| Diff views | Own wiring-diff component (`netlist.json`) · Monaco diff for `firmware/` |
| Local dev | Docker Compose: Postgres + server + website, one command |
| Tests | Go `testing` + a real Postgres in Docker |
| Hosting (when deployed) | AWS: EC2 + EBS (server and repos) · RDS (Postgres) · S3 (big files) · Amplify (website) |

**Tables:** `users`, `repos`, `commits` (hash, repo, author, message, time), `refs` (branch → commit)

**Pages:** repo list → repo page → commit list → commit detail (wiring diff + code diff)

**Order:** `server/push-pull` → `server/postgres` → `server/api` → `web/pages` → `infra/docker`
(starts after Part 1 step 11: push/pull between two local folders)

**Done when:** one person runs `kirch push`, another runs `kirch pull`, and both see the commit on the website.

## Part 3 — Probe (Wi-Fi only, no USB)
- ESP32-S3, JSON readings, battery powered
- Wi-Fi setup over Bluetooth from a phone (first time only)
- Found on the network as `kirch-probe.local` (mDNS)
- Streams readings over WebSocket; numbered so dropped ones are re-sent
- `kirch probe pair` → pair once with a shared key
- `kirch capture` → `capture.json`, attach to commits
- Fallback: probe creates its own Wi-Fi network when the local one blocks devices
- Probe firmware updates over Wi-Fi (OTA) — USB is only used once, to flash the very first firmware

## Part 4 — Agent
- `kirch debug "<problem>"` — reads commits + probe, narrows cause, suggests fix
- `kirch fix` — proposes fix as new branch
- `kirch explain` — wiring diff in plain English
- `kirch bisect --auto` — finds commit that broke a signal
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
- Deploy to AWS (EC2, RDS, S3, Amplify — see Part 2 stack)
- Big files (photos, binaries) → S3
- Pull requests, comments, auth
- KiCad import
- Network protocol hardening (auth, resume, large repos)

## Open Questions
- ~~Exact `netlist.json` format — blocks step 8~~ → decided 2026-11-13 (`docs/formats.md`)
- ~~Merge strategy for netlists — blocks step 10~~ → v1 decided 2026-11-13 (per pin)
- Which LLM model / cost limits for agent — blocks Part 4
- ~~Hosting choice — blocks Part 2~~ → decided 2026-11-13 (Docker locally, AWS when deployed)
- Pin names per board model (`U1.GPIO9` vs `U1.9`) — see `docs/formats.md`
