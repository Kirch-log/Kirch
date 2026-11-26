# Kirch Platform — Plan (draft 2: circuit first)

The website people use to share and understand circuits.
This is track **A** in [`PLAN.md`](../PLAN.md). Nothing here is final until we both tick it in section 10.

---

## 1. The idea in one line

**Kirch shows what changed in a circuit — and whether it still works.**
GitHub shows files and text. Kirch shows wires, parts and real measurements.

## 2. Why this version (and not a GitHub copy)

- A GitHub copy is mostly accounts, settings and file lists — lots of work, nothing new.
- Kirch repos already work with git, so plain hosting adds little on its own.
- What nobody else has: **wiring diffs, wire history, and probe readings per commit.**
- So v1 = a few pages that show those really well, and as little plumbing as possible.
- Look and words stay GitHub-like (header, tabs, "Commits", "Branches"), so it feels familiar.

## 2b. How circuits get in: the user writes nothing

- The user plugs their ESP into our probe (one USB cable). That's all they do.
- Kirch reads their ESP code (`servo.attach(9)` → servo on GPIO9) and the live data from their ESP (50 Hz pulses on GPIO9 → a servo).
- From both it makes a draft circuit (`parts.json` + `netlist.json`); the user confirms "is this right?" with one click.
- Kirch draws the circuit itself (auto-layout, 3D digital twin); a camera above the bench (optional) adds where parts really sit.
- No KiCad or other drawing tools (a KiCad import was built before this decision; it's parked).
- Who it's for: makers and students building ESP32 projects.

## 3. The 2-minute demo (what v1 must make possible)

1. Plug an ESP into the probe → Kirch works out the circuit; open the repo → see it drawn, with parts and nets.
2. Open the commit list → each commit says what changed in wires ("servo moved GPIO9 → GPIO10").
3. Open one commit → changed wires highlighted on the drawing, then parts, then code.
4. Click a pin → its whole history: every commit that moved it.
5. Open readings → the servo signal measured on each commit; see exactly where it broke.

If a feature doesn't help this demo, it waits for v2.

---

## 4. v1 pages

**Home `/`**
- Logged out: what Kirch is + newest public repos.
- Logged in: your repos + recent pushes in them.

**Circuit page `/{owner}/{repo}`** (the repo home)
- Big drawing of the circuit: parts as boxes, nets as coloured lines.
- Click a part → its pins and what each connects to.
- Click a net → every pin on it.
- Side card: number of parts, nets, last commit, branch picker, clone command.
- Below: README and the file list (like GitHub).

**Commits `/{owner}/{repo}/commits`**
- List of commits, newest first.
- Each row: message, author, time, and a one-line wiring summary ("2 wires moved, 1 part added").

**One commit `/{owner}/{repo}/commit/{id}`**
- The drawing with changes highlighted: added wires green, removed red, moved pins orange.
- Wiring diff in words (`SERVO1.SIG: U1.GPIO9 → U1.GPIO10`).
- Parts added / removed / changed.
- Firmware code diff (normal side-by-side diff).
- Probe readings saved with this commit, if any.

**Wire history `/{owner}/{repo}/pin/{PART.PIN}`** — "git blame for wires"
- Every commit that connected, moved or disconnected this pin, newest first.
- Readings for that pin over the same commits, when there are any.

**Branches + compare `/{owner}/{repo}/branches`, `/compare/{a}...{b}`**
- Branch list with "ahead / behind main".
- Compare = the same view as one commit, but between two branches.
- Shows "can merge" or which nets would conflict.

**Readings `/{owner}/{repo}/readings`** (needs `kirch capture`, Part 3)
- One chart per probe pin across commits.
- Click a point → that commit.

**File view `/{owner}/{repo}/blob/{branch}/{path}`**
- Circuit files shown nicely (parts table, net list), with a "raw" button.
- Other files shown as text.

**Your settings** — just one thing in v1: make / delete tokens for `kirch push`.

---

## 5. People and logins (kept small)

- **Log in with GitHub** — no passwords, resets or email checks to build.
- Everyone has a username (their GitHub one) and a profile page listing their repos.
- **Tokens** for the command line: made in settings, shown once, used by `kirch push`.
- **All repos are public in v1.** Anyone can view and clone; only the owner and the people they add can push.
- Private repos, teams and roles come in v2.

## 6. What v1 does NOT have (on purpose)

- Pull requests, issues, comments
- Private repos, organisations, teams
- Stars, forks, search, notifications
- Passwords / email login
- 3D (Part 5) — v1 drawing is 2D

---

## 7. The API (what the website asks the server for)

About 15 calls instead of 30. All JSON, all read-only except login, tokens, new repo and pushers.

| Call | Gives back |
|---|---|
| `GET /api/me` | who's logged in (or nothing) |
| `GET /api/login/github` · `POST /api/logout` | log in with GitHub / log out |
| `GET/POST/DELETE /api/me/tokens` | your tokens (secret shown once on create) |
| `GET /api/explore` | newest public repos |
| `GET /api/users/{user}` | profile + their repos |
| `POST /api/repos` | new empty repo (name, description) |
| `PUT/DELETE /api/repos/{o}/{r}/pushers/{user}` | let someone push / stop them |
| `GET /api/repos/{o}/{r}` | repo + circuit card (parts, nets, last commit, branches) |
| `GET /api/repos/{o}/{r}/circuit?ref=` | the circuit at a commit: parts, pins, nets (for the drawing) |
| `GET /api/repos/{o}/{r}/commits?ref=&pin=` | commit list with wiring summaries (`pin=` → wire history) |
| `GET /api/repos/{o}/{r}/commits/{id}` | one commit: wiring changes, part changes, code diff, readings |
| `GET /api/repos/{o}/{r}/compare/{a}...{b}` | same as one commit, between two refs + can-merge |
| `GET /api/repos/{o}/{r}/branches` | branches with ahead / behind |
| `GET /api/repos/{o}/{r}/files/{ref}/{path}` | a folder listing or a file |
| `GET /api/repos/{o}/{r}/readings?pin=` | probe readings per commit |

Exact JSON for each: we write it together once these are agreed (section 10).

---

## 8. How it fits Evin's server (track B)

- Push / pull: Evin's own TCP protocol (`kirch://`) — unchanged.
- After every push, the indexer saves into Postgres: commits, branches, and the **wiring summary + pin changes of each commit** (so commit lists and wire history are fast).
- Repo folders stay the source of truth; Postgres is only an index.
- The website talks only to the HTTP API above.

Database tables for v1: `users`, `tokens`, `repos`, `pushers`, `commits` (with wiring summary), `pin_changes` (commit, pin, old net, new net), `branches`, `readings`.

## 9. Engine work needed (command-line side)

- Circuit summary of a commit: parts, pins, nets → for the drawing. **(new)**
- Pin changes of a commit (from the wiring diff we already have). **(small)**
- Parts diff: parts added / removed / changed. **(new)**
- Ahead / behind for branches, and a "can these merge?" check without touching files. **(small — reuse merge)**
- Code diff as JSON lines for the website. **(small)**
- Draft circuit from the user's ESP code + data (see `PLAN.md` track D). **(new — first)**
- Drawing: Kirch's own auto-layout (`layout.json`), with positions from the camera when there is one. **(new)**

---

## 10. Decide together (tick when agreed)

- [ ] Circuit first (this draft) instead of a GitHub copy
- [ ] Circuits come from the user's ESP (code + data), not from drawing tools
- [ ] Log in with GitHub only in v1
- [ ] All repos public in v1
- [ ] The v1 pages in section 4 (cut or add any)
- [ ] The ~15 API calls in section 7
- [ ] Readings page right after Part 3 (`kirch capture`), not in the first v1 demo
- [ ] Website name + domain
- [ ] Limits: max repo size (suggest 100 MB), max file (10 MB)

## 11. Build order

1. Agree section 10 (both).
2. Draft circuit from ESP code + data, then engine helpers from section 9 (Hridhayesh).
3. Server: login, tokens, repos, indexer, API (Evin).
4. Website pages, using fake data until the API is ready (Hridhayesh).
5. Readings page once `kirch capture` works (Part 3).

**v1 is done when:** someone logs in with GitHub, pushes a circuit, and anyone can open the circuit page, a commit with highlighted wires, and the history of one pin.

## 12. Later

- **v2 — work together:** pull requests with comments on wires, private repos, teams, issues.
- **v3 — hardware:** circuit-rule checks on every push, releases as board revisions (Rev A, Rev B), 3D view + 3D diff, agent explaining each pull request.
