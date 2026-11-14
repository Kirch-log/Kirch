# Kirch — Plan

**Git for hardware circuits.** Version your circuit, compare versions, go back, and debug the real board with a probe + AI agent.

Personal project. Core pieces built **from scratch**.

**Status: plan locked 2026-11-12.** Changes from here = new decision entry.

**How we use this file:** one checklist for both of us. Whoever does an item ticks it.
`[x]` done · `[~]` built, not tested yet · `[ ]` to do.
Work happens on branches; nothing is merged into `main` until the end.

---

## The 5 Parts

| # | Part | Role | Goal |
|---|---|---|---|
| 1 | **`kirch` CLI** | Memory | Own git: commit, branch, diff, checkout, revert, merge, push |
| 2 | **Platform** | Sharing | GitHub-like server + website |
| 3 | **Probe** | Eyes | ESP32-S3 reads real board, over Wi-Fi |
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
- **Index:** simple JSON (path → hash, mode, mtime, size) — not git's binary DIRC; file is `.kirch/index.json`
- **Agent never changes history alone** — suggests fixes as branches, user confirms
- Old ReWeird code ignored — fresh start

## Decision Log

| Date | Decision |
|---|---|
| 2026-11-13 | **Data formats** set in [`docs/formats.md`](docs/formats.md): `parts.json`, `netlist.json`, `layout.json`, `capture.json`. Netlist merge v1 = per pin, conflict only when both sides move the same pin. |
| 2026-11-13 | **Part 2 stack + hosting** chosen (see Part 2). Local Docker first, AWS when deployed. |
| 2026-11-13 | **One shared checklist**, no per-person split. Branches only; merge into `main` at the end. |
| 2026-11-13 | **Kirch-only files get a `.json` name** (`index.json`, `config.json`) so real git can read a `.kirch` folder (`git --git-dir=.kirch log`, `fsck --strict` pass). |

---

## Storage (git model)

```
.kirch/
├── HEAD           → current branch
├── index.json     → staging area (JSON)
├── config.json    → repo settings (user.name / user.email)
├── refs/heads/    → branches (file = commit hash)
└── objects/       → blobs, trees, commits (SHA-1 named, zlib)
```

**A commit stores:** `netlist.json`, `parts.json`, `layout.json`, `firmware/`, `capture.json` (probe readings).

---

## Checklist

### 0 — Foundations
- [x] Plan (`PLAN.md`)
- [x] Data formats — [`docs/formats.md`](docs/formats.md)
- [x] Part 2 stack + hosting decided
- [x] Repo setup: `go.mod`, `main.go` + `cmd/` + `internal/`, `.gitignore`, CI (Go build/vet/fmt/test, firmware tests + compile)

### Part 1 — `kirch` CLI

```
kirch/
├── main.go
├── cmd/        ← Cobra commands (thin)
└── internal/   ← engine: repo, objects, index, refs, diff, merge
```

Per step: engine code → test → Cobra command → try it → compare with real git.

- [x] 1. Object store — hash, compress, save, read (`internal/objects`)
  - `Hash(type, data)`, `Store.Write(type, data) → id` — header + SHA-1 + zlib, `.kirch/objects/ab/cdef…`
  - `Store.Read(id) → (type, data)` — decompress, check header, size and hash
  - Tests: our ids == `git hash-object`; git reads our objects and we read git's
  - Commands: `kirch hash-object [-w]`, `kirch cat-file -t|-s|-p`
  - Blobs first; trees + commits come with `commit` step
- [x] 2. `init` (`internal/repo`: `Init`, `Find` from any subfolder; git accepts `.kirch` as a git dir)
- [x] 3. `add` + index (`internal/index`, `internal/canon`, `Repo.Add`; `kirch add`, `kirch ls-files`)
  - Circuit files checked + rewritten to canonical form; one bad file → nothing staged, nothing rewritten
  - Folders recursive, deleted files dropped, `.kirch`/`.git` skipped
  - Tests: `kirch ls-files` == `git ls-files -s` for the same files
- [x] 4. `commit` (tree + commit objects, `internal/refs` with lock + compare-and-swap, `kirch commit -m`, `kirch config [--global] user.name|user.email`)
  - Tests: tree and commit ids == `git write-tree` / `git commit-tree`; `git log` and `git fsck --strict` accept `.kirch`
- [x] 5. `log` 🎯 *working history tool* (`Repo.Log`, `Repo.Resolve`; `kirch log [rev] [-n N] [--oneline]`)
  - Revisions: `HEAD`, branch name, full or short (4+ chars) commit id
  - Order = git's default (newest committer date first), merges included
  - Tests: commit order == `git rev-list`; printed output == `git log --decorate=short` byte for byte
- [x] 6. `status` (`Repo.Status`, `Repo.TreeFiles`; `kirch status [-s|--porcelain]`)
  - Staged (last commit → index), not staged (index → files), untracked (folder with nothing tracked = `dir/`)
  - Circuit files compared in canonical form: only reformatted = not changed
  - Files with unchanged size + mtime skip re-hashing, except ones as new as the index
  - Tests: `kirch status --porcelain` == `git status --porcelain` byte for byte (same commit + index)
- [x] 7. `branch` / `checkout` (`Repo.CreateBranch`, `DeleteBranch`, `Checkout`, `IsAncestor`)
  - `kirch branch [name [start]]`, `-d` (only if merged) / `-D`; `kirch checkout <branch>`, `checkout -b <new> [start]`
  - Checkout keeps uncommitted changes to files that are the same on both branches; refuses (changes nothing) if a changed file differs or an untracked file is in the way
  - Checking out a bare commit id (detached HEAD) not supported — make a branch from it instead
  - Tests: `kirch branch` == `git branch`; after checkout index + files == `git ls-tree -r`, `git symbolic-ref HEAD` agrees, `git fsck` passes
- [x] 8. `diff` + **wiring diff** for netlist 🎯 (`internal/diff`: own Myers line diff, git patch format, `Wiring()`; `Repo.DiffWorktree/DiffStaged/DiffCommits`)
  - `kirch diff` (files vs index), `--staged`, `<commit>`, `<a> <b>`, optional paths; `--text` = plain patch for every file
  - `netlist.json` shown as wiring: nets added / removed / renamed, pin moved between nets, pin swapped (`SERVO1.SIG: U1.GPIO9 → U1.GPIO10`), pin connected / disconnected
  - Tests: `kirch diff --text` == `git diff` byte for byte (all four modes + paths); `git apply` of our patches rebuilds the new files; line diff always shortest
- [ ] 9. `reset` / `revert`
- [ ] 10. `merge` (netlist: per pin)
- [ ] 11. `push` / `pull` / `clone` to local folder 🎯 *Part 1 done*

### Part 2 — Platform

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

Tables: `users`, `repos`, `commits` (hash, repo, author, message, time), `refs` (branch → commit).
Starts after Part 1 step 11.

- [ ] Server push / pull over HTTP
- [ ] Postgres tables + migrations, commit summaries written on push
- [ ] API for the website: repos, commits, one commit, diff
- [ ] Website: repo list → repo page → commit list → commit detail (wiring diff + code diff)
- [ ] Docker Compose: everything with one command
- [ ] 🎯 *Part 2 done:* one person `kirch push`es, another `kirch pull`s, both see the commit on the website

### Part 3 — Probe (Wi-Fi only, no USB)

Firmware in [`firmware/`](firmware/README.md).

- [~] Probe firmware: reads 6 probes (analog / digital / pulse), one reading per second
- [~] Wi-Fi setup over Bluetooth from a phone (first time only)
- [~] Found on the network as `kirch-probe.local` (mDNS)
- [~] Streams readings over WebSocket; numbered so dropped ones are re-sent
- [x] Laptop tools: `probe_listen.py`, `fake_probe.py` (test without hardware)
- [ ] Tested on the real ESP32 (checklist in `firmware/README.md`)
- [ ] Pairing with a shared key → `kirch probe pair`
- [ ] Probe firmware updates over Wi-Fi (OTA) — USB only for the very first flash
- [ ] Fallback: probe creates its own Wi-Fi when the local one blocks devices
- [ ] Battery level + low-battery warning, OLED screens
- [ ] Hardware timers for very short pulses (10 µs HC-SR04 trigger)
- [ ] `kirch capture` → `capture.json`, attached to commits
- [ ] `kirch verify` — live board vs current commit
- [ ] 🎯 *Part 3 done:* a commit includes real readings sent over Wi-Fi

### Part 4 — Agent
LLM (Claude) + tool use; tools = our engine + probe.

- [ ] `kirch explain` — wiring diff in plain English
- [ ] `kirch debug "<problem>"` — reads commits + probe, narrows cause, suggests fix
- [ ] `kirch fix` — proposes fix as new branch
- [ ] `kirch bisect --auto` — finds commit that broke a signal
- [ ] 🎯 *Part 4 done:* `kirch debug` finds a real cause from history + probe readings

### Part 5 — 3D View
- Main way to see a commit on the website
- Built in the browser (React Three Fiber / Three.js) from `netlist.json` + `layout.json` — no 3D file stored in commits
- Part models in glTF (converted from KiCad's free 3D library)
- Photo only as last resort, for what 3D can't show (crossed, short or rigid wires)

- [ ] 2D breadboard editor to enter the layout
- [ ] 3D viewer
- [ ] 3D diff: added wires green, removed red, moved parts highlighted
- [ ] Live probe values on the wires (green/red per connection)
- [ ] 🎯 *Part 5 done:* every commit on the website shows a 3D model you can rotate

### Later (after Part 5)
- [ ] Deploy to AWS (EC2, RDS, S3, Amplify — see Part 2 stack)
- [ ] Big files (photos, binaries) → S3
- [ ] OTA firmware updates for the circuit board
- [ ] Cloud mode (AWS IoT Core)
- [ ] Pull requests, comments, auth
- [ ] KiCad import
- [ ] Network protocol hardening (auth, resume, large repos)
- [ ] Merge all branches into `main`

---

## Open Questions
- ~~Exact `netlist.json` format — blocks step 8~~ → decided 2026-11-13 (`docs/formats.md`)
- ~~Merge strategy for netlists — blocks step 10~~ → v1 decided 2026-11-13 (per pin)
- Which LLM model / cost limits for agent — blocks Part 4
- ~~Hosting choice — blocks Part 2~~ → decided 2026-11-13 (Docker locally, AWS when deployed)
- Pin names per board model (`U1.GPIO9` vs `U1.9`) — see `docs/formats.md`
