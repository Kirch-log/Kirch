# Kirch — Plan

**Git for hardware circuits.** Version your circuit, compare versions, go back, and debug the real board with a probe + AI agent.

Personal project. Core pieces built **from scratch**.

**Status: plan locked 2026-11-12.** Changes from here = new decision entry.

---

## The 4 Parts

| # | Part | Role | Goal |
|---|---|---|---|
| 1 | **`kirch` CLI** | Memory | Own git: commit, branch, diff, checkout, revert, merge, push |
| 2 | **Platform** | Sharing | GitHub-like server + website |
| 3 | **Probe** | Eyes | ESP32-S3 reads real board |
| 4 | **Agent** | Brain | Uses history + probe to find problems and suggest fixes |

---

## Decisions Made

- **Name:** `kirch`, data in `.kirch/`
- **Git engine:** from scratch (no go-git)
- **Language:** Go
- **Go module:** `github.com/evinbento/kirch`
- **Cobra:** command parsing only — all logic ours
- **Engine = clean Go functions** (`Commit()`, `Diff()`, `Log()`) so CLI *and* agent can call them
- **Object format:** git-identical (`<type> <size>\0<content>`, zlib, same tree/commit layout) → hashes match `git hash-object`, real git = test oracle
- **Hash:** SHA-1 (git default)
- **Index:** simple JSON (path → hash, mode, mtime, size) — not git's binary DIRC
- **Agent never changes history alone** — suggests fixes as branches, user confirms
- Old ReWeird code ignored — fresh start

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

## Part 3 — Probe
- ESP32-S3, JSON readings
- USB serial first, Wi-Fi later
- `kirch capture` → `capture.json`, attach to commits

## Part 4 — Agent
- `kirch debug "<problem>"` — reads commits + probe, narrows cause, suggests fix
- `kirch fix` — proposes fix as new branch
- `kirch explain` — wiring diff in plain English
- `kirch bisect --auto` — finds commit that broke a signal
- LLM (Claude) + tool use; tools = our engine + probe

---

## Later (plan after Part 4)

- 3D circuit viewer + 3D diff
- Wi-Fi probe: pairing, mDNS, WebSocket, battery
- OTA firmware updates (probe + circuit board)
- Cloud mode (AWS IoT Core)
- Deployment / hosting (AWS or simpler)
- Big files (photos, binaries) → S3-style storage
- Pull requests, comments, auth
- KiCad import
- Network protocol hardening (auth, resume, large repos)

## Open Questions
- Exact `netlist.json` format — blocks step 8
- Merge strategy for netlists (conflicts in wiring?) — blocks step 10
- Which LLM model / cost limits for agent — blocks Part 4
- Hosting choice — blocks Part 2
