# Kirch Platform — Product Plan (draft v1)

What the Kirch website has, page by page, and the API it reads from.
This is track **A** in [`PLAN.md`](../PLAN.md); the backend (track B) builds
the API in section 7, and the frontend (track C) builds the pages in section 5.

**Rule of thumb: if GitHub has it, Kirch has it in the same place, with the
same name and the same URL.** Someone who knows GitHub should never need help.
What's different is *what we show*: a circuit change is shown as wiring
first, code second.

---

## 1. Principles

1. **Same as GitHub** — same URLs, same page layout (header, repo tabs,
   file browser, commit list, branch picker), same words ("Code", "Commits",
   "Pull requests", "Issues", "Settings", "Clone").
2. **Circuit first** — wherever GitHub shows a text diff, we show the
   **wiring diff** at the top (`SERVO1.SIG: U1.GPIO9 → U1.GPIO10`), then
   parts, then firmware code.
3. **One engine** — the server uses the same Go engine as the `kirch` CLI
   (`internal/repo`, `internal/diff`, `internal/merge`). A diff on the
   website is exactly what `kirch diff` prints.
4. **Small v1, then grow** — v1 is "browse and share". Collaboration (pull
   requests, issues) is v2. Hardware extras (checks, releases, probe data,
   3D) are v3.

---

## 2. Versions at a glance

| | **v1 — Browse & share** | **v2 — Collaborate** | **v3 — Hardware** |
|---|---|---|---|
| People | Sign up, log in, profile page, access tokens for the CLI | Organizations + teams, notifications | — |
| Repos | Create, public / private, settings, delete, collaborators | Stars, watch, forks | Topics, templates ("ESP32 starter") |
| Code | File browser, README, circuit file views, raw files | Search in a repo | BOM export (CSV) |
| History | Commits, one commit (wiring + code diff), branches, compare | Blame (who changed each net) | Readings over time (probe) |
| Working together | — | **Pull requests** (review, comment on wires, merge), **Issues** | Agent comments on PRs ("explain this change") |
| Quality | — | Protected branches | **Checks** (circuit rules, `kirch verify` results) |
| Releases | — | Tags | **Releases** = hardware revisions (Rev A, Rev B) with files |
| 3D | — | — | 3D view + 3D diff on every commit (Part 5) |

---

## 3. People and permissions

### Accounts (v1)
- Sign up with **username, email, password**. Username rules = GitHub's:
  1–39 chars, letters, digits and single hyphens, not starting/ending with `-`.
- Log in on the website → session cookie.
- **Personal access tokens** for the CLI: made in Settings → Tokens, shown once.
  `kirch push` sends `username + token` (HTTP basic auth, like GitHub).
- Profile page: avatar (Gravatar in v1, upload in v2), name, bio, repos.

### Who can see and do what
| | Anyone (not logged in) | Logged-in user | **Read** collaborator | **Write** collaborator | **Admin** / owner |
|---|---|---|---|---|---|
| See / clone a **public** repo | ✓ | ✓ | ✓ | ✓ | ✓ |
| See / clone a **private** repo | ✗ | ✗ | ✓ | ✓ | ✓ |
| Push, create / delete branches | ✗ | ✗ | ✗ | ✓ | ✓ |
| Open PRs / issues, comment (v2) | ✗ | public repos | ✓ | ✓ | ✓ |
| Merge PRs (v2) | ✗ | ✗ | ✗ | ✓ | ✓ |
| Settings, collaborators, delete repo | ✗ | ✗ | ✗ | ✗ | ✓ |

- A private repo you can't see returns **404, not 403** (like GitHub — we
  don't reveal that it exists).
- v1 roles are exactly these three. GitHub's Triage / Maintain come later if needed.

---

## 4. URL map

Same paths as GitHub. `{ref}` is a branch name or commit id; branch names
with `/` (e.g. `feature/servo`) are resolved by trying the longest branch
name that exists, like GitHub.

| URL | Page | Ver |
|---|---|---|
| `/` | Logged out: landing page. Logged in: dashboard | v1 |
| `/login`, `/signup` | Log in / sign up | v1 |
| `/new` | Create a repository | v1 |
| `/explore` | Public repos (recent, later most-starred) | v1 |
| `/{user}` | Profile: repos, (v2) stars | v1 |
| `/settings/profile`, `/settings/tokens` | Your account | v1 |
| `/{owner}/{repo}` | Repo home (Code tab) | v1 |
| `/{owner}/{repo}/tree/{ref}/{path}` | Folder | v1 |
| `/{owner}/{repo}/blob/{ref}/{path}` | File (with circuit views) | v1 |
| `/{owner}/{repo}/raw/{ref}/{path}` | Raw file bytes | v1 |
| `/{owner}/{repo}/commits/{ref}` | Commit list (`?path=` for one file's history) | v1 |
| `/{owner}/{repo}/commit/{sha}` | One commit | v1 |
| `/{owner}/{repo}/branches` | Branches | v1 |
| `/{owner}/{repo}/compare/{base}...{head}` | Compare two branches / commits | v1 |
| `/{owner}/{repo}/settings` | General settings | v1 |
| `/{owner}/{repo}/settings/access` | Collaborators | v1 |
| `/{owner}/{repo}/pulls`, `/pull/{n}`, `/pull/{n}/files`, `/pull/{n}/commits` | Pull requests | v2 |
| `/{owner}/{repo}/issues`, `/issues/{n}`, `/issues/new` | Issues | v2 |
| `/{owner}/{repo}/blame/{ref}/{path}` | Blame | v2 |
| `/{owner}/{repo}/stargazers`, `/forks` | Stars, forks | v2 |
| `/orgs/{org}`, `/orgs/{org}/teams` | Organizations | v2 |
| `/notifications` | Notifications | v2 |
| `/{owner}/{repo}/releases`, `/releases/tag/{tag}` | Releases (hardware revisions) | v3 |
| `/{owner}/{repo}/commit/{sha}/checks` | Checks on a commit | v3 |
| `/{owner}/{repo}/readings` | Probe readings over time | v3 |

**Clone URL:** `https://<host>/{owner}/{repo}` — used as
`kirch clone https://<host>/ana/servo-board` and `kirch remote add origin …`.

---

## 5. Pages — what each one shows (v1)

### Global header (every page)
Logo · search box (v1: jumps to repos/users by name) · `+` menu (New repository) ·
avatar menu (Your profile, Your repositories, Settings, Sign out).
Logged out: "Sign in" / "Sign up".

### Dashboard `/` (logged in)
- Left: **your repositories** (filter box, "New" button).
- Middle: **recent activity** in repos you're in: "ana pushed 3 commits to
  `main` in ana/servo-board" with the first commit messages. (From push events,
  section 8.)
- Empty state: "Create your first repository" + install instructions for `kirch`.

### New repository `/new`
- Owner (you; orgs in v2), name, description, **Public / Private**.
- Option: "Start with a circuit template" → creates first commit with
  `README.md`, empty `parts.json` and `netlist.json` (canonical form).
- After create, an **empty repo** shows GitHub's "Quick setup" box:
  ```
  kirch remote add origin https://<host>/ana/servo-board
  kirch push -u origin main
  ```

### Repo home `/{owner}/{repo}` — the Code tab
Same layout as GitHub:
- **Top:** `owner / repo` + Public/Private badge. (v2: Watch · Fork · Star.)
- **Tabs:** Code · (v2) Issues · Pull requests · (v3) Checks · Releases · Readings · Settings (admins).
- **Bar above files:** branch picker (search + list) · "N branches" link ·
  "Code" button → clone URL with copy button + `kirch clone` command.
- **Latest commit bar:** avatar, author, message, short id, time ago, "N commits" link.
- **File list:** folders first, then files; for each: name, message of the last
  commit that touched it, time ago.
- **README.md** rendered below (GitHub-flavored Markdown).
- **Right sidebar "About":** description, and a **Circuit card** (Kirch-only):
  - board type, main controller (`U1: ESP32-S3-DevKitC-1`)
  - counts: parts, nets, connected pins
  - last probe capture: time + "all readings normal / 2 changed" (v3)
  - 3D thumbnail (v3)

### Folder `/tree/{ref}/{path}`
Same as the file list above, with a breadcrumb (`servo-board / firmware / src`).

### File `/blob/{ref}/{path}`
- Header: breadcrumb, last commit for this file, lines + size, buttons:
  **Raw · Copy · History** (→ commits for that path).
- Body depends on the file. Every circuit file has **Circuit view | JSON** toggle:

| File | Circuit view (default) |
|---|---|
| `netlist.json` | **Nets table**: one row per net — name, pins as chips (`U1.GPIO9`). Click a pin → highlight every net row it's in. Filter box. |
| `parts.json` | **Parts list (BOM)**: ID, type, model, value, pins; grouped count by model ("2 × resistor 1k"). |
| `layout.json` | 2D breadboard picture (v1: simple SVG grid with parts and wires); 3D in v3. |
| `capture.json` | **Readings table**: probe, net, mode, values (`4.98 V avg`, `1500 µs pulse`), device + time. |
| `firmware/**` and other text | Code with line numbers and syntax highlighting. |
| Images | Shown inline. |
| Other binary | "Binary file — Download". |

### Commit list `/commits/{ref}`
- Branch picker + (optional) path filter shown as "History for firmware/src/main.cpp".
- Grouped by day ("Commits on Nov 14, 2026"), newest first, 30 per page, "Older / Newer".
- Each row: message (first line; expand "…" for the rest), avatar + author,
  time ago, short id with copy button, **"Browse files"** (→ tree at that commit), and
  Kirch **badges**:
  - 🔌 `3 wiring changes` · 🧩 `+1 part` · 💾 `firmware` · 📈 `capture` (v3) · ✓/✗ checks (v3)

### One commit `/commit/{sha}`
- **Header:** full message, author, time, "N parents: abc1234 (+ def5678)",
  full sha + copy, "Browse files".
- **Summary line:** "Showing **4 changed files** with **12 additions** and **3 deletions**" +
  **"3 wiring changes"**.
- **Sections in this order:**
  1. **Wiring changes** (if `netlist.json` changed) — a list, one line per
     change, color-coded:
     - 🟢 `+ net US_TRIG: U1.GPIO3, US1.TRIG`
     - 🔴 `- net OLD: …`
     - 🟡 `SERVO1.SIG: U1.GPIO9 → U1.GPIO10` (swap) / `U1.GPIO4 moved from net GND to net 5V`
     - `net GND renamed to GROUND`
     - "View as JSON diff" toggle → the plain text diff.
  2. **Parts changes** (if `parts.json` changed): `+ R3 resistor 1k`, `- LED2`,
     `R1 value: 1k → 2k`.
  3. **Readings changes** (if `capture.json` changed, v1 as a table of old vs new).
  4. **Files** — every changed file like GitHub: collapsible card per file
     (status A/M/D, path, `+12 −3`), **Unified | Split** toggle, line numbers,
     "Load diff" for big files, "Binary file not shown".
- File tree on the left listing changed files (jump links), like GitHub.

### Branches `/branches`
- Default branch on top, then the rest (Yours / Active / Stale tabs in v2).
- Each row: name (link), last commit time + author, **"N behind | M ahead"** of the
  default branch, (v2) PR link, delete 🗑 (write access; not the default branch).
- "New branch" button: name + "from" branch.

### Compare `/compare/{base}...{head}`
- Two branch pickers (base ← head).
- "✓ Able to merge" / "✗ Can't automatically merge" (uses the netlist merge; v1 shows
  the status only).
- Commits list (like the commit list), then the **combined diff** in the same
  layout as one commit (wiring → parts → readings → files).
- v2: green **"Create pull request"** button.

### Repo settings `/settings` (admins)
- **General:** name (rename), description, default branch, visibility.
- **Danger zone:** change visibility, (v2) transfer, **delete** (type the repo name to confirm).
- **Collaborators** `/settings/access`: list (avatar, username, role), add by username
  with role Read / Write / Admin, change role, remove.

### Profile `/{user}`
Avatar, name, bio, "Repositories" list (public ones; private too if it's you),
(v2) stars and pinned repos.

### Account settings
- `/settings/profile`: name, bio, email, avatar (Gravatar).
- `/settings/tokens`: list (name, created, last used), **Generate new token**
  (name + expiry), shown once, delete.

---

## 6. v2 and v3 pages (short form — detailed later)

### Pull requests (v2)
- List `/pulls`: Open / Closed tabs, title, `#n`, author, branch, labels, review state.
- `/pull/{n}` tabs like GitHub: **Conversation · Commits · Checks · Files changed**.
- **Files changed** = the compare view; reviewers can **comment on a wiring change**
  (anchored to the net or pin, e.g. "Is GPIO10 free on this board?") and on code lines.
- Reviews: Comment / Approve / Request changes.
- **Merge button** merges on the server with the same net-by-net netlist merge as
  `kirch merge`. If there's a conflict: "This branch has conflicts" + steps to fix with the CLI
  (`kirch pull origin main` → fix → `kirch push`). Merge commit only at first; squash later.
- Engine needed: merge two commits **without a working folder** (in memory) —
  `planMerge` already does the hard part.

### Issues (v2)
Title, Markdown body, labels, assignees, open/closed, comments. Hardware
templates: "Board doesn't work" (attach a capture, what you expected).
`fixes #12` in a commit message closes the issue when pushed to the default branch.

### Organizations (v2)
`/orgs/{org}` owns repos; members + teams; team access to repos (Read/Write/Admin).

### Checks (v3) — Kirch's version of GitHub Actions status
Run on the server after every push; result ✓ / ✗ on commits, branches, PRs:
- netlist is valid (no pin in two nets — `kirch add` already enforces it)
- every pin in `netlist.json` belongs to a part in `parts.json`
- `layout.json` connections match `netlist.json` (docs/formats.md)
- simple electrical rules: power shorted to GND, two outputs on one net, unconnected required pins
- latest `kirch verify` result (live board vs commit), if uploaded

### Releases (v3)
A release = a tagged commit called a hardware revision ("Rev B"): notes + files
(firmware `.bin`, BOM CSV, gerbers later). Needs `kirch tag` in the CLI first.

### Readings (v3, Part 3)
Pick a net → chart of its probe value across commits ("SERVO_SIG pulse width per commit").

### 3D (v3, Part 5)
3D tab on commit and blob pages; 3D diff on commit/compare/PR pages; thumbnail on repo home.

---

## 7. API v1 — the contract between backend and frontend

### Conventions
- Base: `https://<host>/api/v1`. JSON in, JSON out. Times RFC 3339 UTC (`2026-11-14T10:00:00Z`).
- Names follow GitHub's REST API where one exists (`sha`, `full_name`, `default_branch`, `private`).
- Auth: session cookie (website) or `Authorization: Bearer <token>` (CLI, scripts).
- Errors: HTTP status + `{"error": {"code": "not_found", "message": "Repository not found"}}`.
  Codes: `bad_request`, `unauthorized`, `not_found` (also for private repos you can't see),
  `forbidden`, `conflict`, `validation_failed` (+ `"fields": {"name": "already taken"}`).
- Lists: `?page=1&per_page=30` (max 100) → `{"items": [...], "page": 1, "per_page": 30, "has_more": true}`.
- `{ref}` = branch name or full/short commit id (resolved by the engine, `Repo.Resolve`).

### Endpoints
| Method + path | Returns | Who |
|---|---|---|
| `POST /users` | User (sign up) | anyone |
| `POST /session` / `DELETE /session` | User + cookie / – | anyone |
| `GET /user` | current User | logged in |
| `PATCH /user` | User (name, bio, email) | logged in |
| `GET /user/tokens` · `POST /user/tokens` · `DELETE /user/tokens/{id}` | Token list / new Token (with secret, once) | logged in |
| `GET /users/{user}` | User | anyone |
| `GET /users/{user}/repos` | list of Repo (only ones you can see) | anyone |
| `GET /user/repos` | your repos + repos you collaborate on | logged in |
| `POST /user/repos` | Repo (`name`, `description`, `private`, `template`) | logged in |
| `GET /repos/{o}/{r}` | Repo (+ `circuit` summary, + `permissions` for you) | read |
| `PATCH /repos/{o}/{r}` | Repo (`name`, `description`, `private`, `default_branch`) | admin |
| `DELETE /repos/{o}/{r}` | 204 | admin |
| `GET /repos/{o}/{r}/branches` | list of Branch (with `ahead`, `behind` vs default) | read |
| `POST /repos/{o}/{r}/branches` | Branch (`name`, `from`) | write |
| `DELETE /repos/{o}/{r}/branches/{branch}` | 204 (not the default) | write |
| `GET /repos/{o}/{r}/commits?sha={ref}&path={p}` | list of CommitSummary | read |
| `GET /repos/{o}/{r}/commits/{ref}` | CommitDetail (with files + wiring) | read |
| `GET /repos/{o}/{r}/compare/{base}...{head}` | Compare | read |
| `GET /repos/{o}/{r}/contents/{path}?ref={ref}` | folder: list of Entry · file: FileContent | read |
| `GET /repos/{o}/{r}/readme?ref={ref}` | FileContent of README.md (404 if none) | read |
| `GET /repos/{o}/{r}/raw/{ref}/{path}` | raw bytes, right content type | read |
| `GET /repos/{o}/{r}/events` | list of PushEvent (dashboard) | read |
| `GET /user/events` | PushEvents across your repos | logged in |
| `GET /repos/{o}/{r}/collaborators` | list of {User, role} | admin |
| `PUT /repos/{o}/{r}/collaborators/{user}` | `{"role": "read" \| "write" \| "admin"}` | admin |
| `DELETE /repos/{o}/{r}/collaborators/{user}` | 204 | admin |
| `GET /search/repositories?q=` · `GET /search/users?q=` | lists | anyone |
| `GET /explore` | public repos, newest push first | anyone |

Push / pull / clone are **not** in this table: they're the separate transfer
endpoints the `kirch` CLI talks to (track B, push/pull server).

### Objects

**User**
```json
{ "login": "ana", "name": "Ana Silva", "avatar_url": "https://…", "bio": "", "created_at": "…" }
```

**Repo**
```json
{
  "owner": { "login": "ana", "avatar_url": "…" },
  "name": "servo-board",
  "full_name": "ana/servo-board",
  "description": "Ultrasonic sensor + servo on ESP32-S3",
  "private": false,
  "default_branch": "main",
  "empty": false,
  "clone_url": "https://<host>/ana/servo-board",
  "pushed_at": "2026-11-14T10:00:00Z",
  "created_at": "…",
  "permissions": { "read": true, "write": true, "admin": false },
  "circuit": { "board": "breadboard-830", "controller": "U1: ESP32-S3-DevKitC-1",
               "parts": 6, "nets": 6, "pins": 17 }
}
```
`circuit` is `null` when the default branch has no `parts.json`/`netlist.json`.

**Branch**
```json
{ "name": "feature/servo", "default": false, "commit": CommitSummary, "ahead": 2, "behind": 5 }
```

**CommitSummary**
```json
{
  "sha": "43f23f0e668c17d2510d40b82d8d804a1590e090",
  "message": "Move servo to GPIO10\n\nOld pin was flaky.",
  "author":    { "name": "Ana Silva", "email": "ana@example.com", "date": "…", "login": "ana" },
  "committer": { "name": "Ana Silva", "email": "ana@example.com", "date": "…", "login": "ana" },
  "parents": ["1f99419aa8efa9c3cf9c7fe16467bde8d682f50e"],
  "stats": { "files": 3, "additions": 12, "deletions": 3,
             "wiring_changes": 1, "parts_added": 0, "parts_removed": 0,
             "firmware_changed": true, "has_capture": false }
}
```
`login` is set when the commit email matches a user's email (like GitHub), else `null`.

**CommitDetail** = CommitSummary + `"files": [FileChange]` + `"truncated": false`.

**FileChange**
```json
{
  "path": "netlist.json",
  "status": "modified",                // added | modified | deleted
  "old_sha": "00733d5…", "new_sha": "f1fe9ad…",
  "old_mode": "100644", "new_mode": "100644",
  "additions": 1, "deletions": 1,
  "kind": "netlist",                   // netlist | parts | capture | text | binary
  "wiring": [ WiringChange ],          // kind = netlist only
  "parts":  [ PartChange ],            // kind = parts only
  "hunks":  [ Hunk ],                  // text diff (also for netlist/parts, for the JSON view)
  "too_large": false                   // true → no hunks; frontend shows "Load diff"
}
```

**WiringChange** (straight from `diff.WiringChange` in the engine)
```json
{ "kind": "pin-swapped", "net": "SERVO_SIG", "old_net": "", "pin": "U1.GPIO10",
  "old_pin": "U1.GPIO9", "pins": [], "label": "SERVO1.SIG",
  "text": "SERVO1.SIG: U1.GPIO9 → U1.GPIO10" }
```
`kind` ∈ `net-added`, `net-removed`, `net-renamed`, `pin-moved`, `pin-swapped`, `pin-joined`, `pin-left`.
`text` is the ready-made line (same as `kirch diff`), so the frontend can show it as is.

**PartChange**
```json
{ "kind": "changed", "id": "R1", "field": "value", "old": "1k", "new": "2k",
  "text": "R1 value: 1k → 2k" }
```
`kind` ∈ `added`, `removed`, `changed`. (Engine function to add — section 9.)

**Hunk** (structured, so the frontend can draw both Unified and Split)
```json
{
  "header": "@@ -5,7 +5,7 @@ void setup() {",
  "old_start": 5, "old_lines": 7, "new_start": 5, "new_lines": 7,
  "lines": [
    { "type": "context", "old": 5, "new": 5, "text": "const int TRIG_PIN = 3;" },
    { "type": "del", "old": 6, "new": null, "text": "const int SERVO_PIN = 9;" },
    { "type": "add", "old": null, "new": 6, "text": "const int SERVO_PIN = 10;", "no_newline": false }
  ]
}
```

**Compare**
```json
{ "base": "main", "head": "feature/servo", "merge_base": "<sha>",
  "ahead": 2, "behind": 0, "mergeable": true, "conflicts": [],
  "commits": [CommitSummary], "files": [FileChange] }
```
`conflicts` lists paths (and for netlists, nets) that would conflict.

**Entry** (folder listing)
```json
{ "name": "firmware", "path": "firmware", "type": "dir", "sha": "…",
  "last_commit": { "sha": "…", "message": "Move servo to GPIO10", "date": "…" } }
```
`type` ∈ `dir`, `file`. Sorted folders first, then files.

**FileContent**
```json
{ "name": "netlist.json", "path": "netlist.json", "sha": "…", "size": 412,
  "kind": "netlist", "binary": false, "text": "{\n  \"format\": …",
  "circuit": { …parsed file… },        // for netlist/parts/layout/capture; null otherwise
  "last_commit": CommitSummary }
```
Files over 1 MB: `text` is `null`, frontend links to raw.

**PushEvent**
```json
{ "actor": User, "repo": "ana/servo-board", "branch": "main",
  "before": "<sha>", "after": "<sha>", "commits": [CommitSummary], "created_at": "…",
  "kind": "push" }                     // push | branch-created | branch-deleted
```

---

## 8. What the server records (for the database)

Postgres holds **who / what / when**; file contents and diffs come from the
`.kirch` repo on disk through the engine (and can be cached).

| Table | Columns (main ones) |
|---|---|
| `users` | id, login, name, email, password_hash, bio, created_at |
| `tokens` | id, user_id, name, hash, expires_at, last_used_at |
| `sessions` | id, user_id, expires_at |
| `repos` | id, owner_id, name, description, private, default_branch, pushed_at, created_at |
| `collaborators` | repo_id, user_id, role |
| `refs` | repo_id, branch, sha (copy of the repo's branches, updated on push) |
| `commits` | repo_id, sha, author name/email/date, committer…, message, parents, **stats** (the CommitSummary `stats` fields) |
| `push_events` | id, repo_id, actor_id, branch, before, after, kind, created_at |
| `path_last_commit` | repo_id, ref, path, sha — for "last commit" in file lists (filled on push) |

**On every push** the server: checks the token has Write, receives objects,
moves the branch (compare-and-swap, so two pushes can't overwrite each other),
then records new commits **with stats** (wiring change count etc. computed
once by the engine), the push event, and updates `refs` + `path_last_commit`.

v2 adds: `stars`, `orgs`, `teams`, `pull_requests`, `reviews`, `comments`, `issues`, `labels`, `notifications`.

---

## 9. Engine work needed (CLI side)

| Needed for | Engine function | Status |
|---|---|---|
| file lists, blob pages | read one folder of a tree; read a file at a ref | have `TreeFiles` + `Objects.Read`; add a one-folder helper |
| commit pages | `DiffCommits`, `diff.Wiring`, `diff.Hunks` | ✓ have |
| JSON hunks with line numbers | hunk → `[{type, old, new, text}]` | add (small) |
| parts changes | `diff.Parts(old, new)` → PartChange list | add |
| branch ahead/behind | count commits between two refs | add (uses `MergeBase`) |
| compare "able to merge" | dry-run merge of two commits, no working folder | add (wrap `planMerge`) |
| commit stats on push | one function returning the `stats` object | add |
| circuit card | parse `parts.json` / `netlist.json` / `layout.json` summary | add |
| PR merge (v2) | merge two commits in memory and write the merge commit | add (v2) |
| releases (v3) | `kirch tag` + tags in refs | add (v3) |

---

## 10. Build order

1. **This doc agreed** (both of us) — especially section 7.
2. **Backend v1 API** (Evin) against section 7, with the engine helpers in section 9.
3. **Probe** (Part 3) and **3D** (Part 5) — they add the v3 pages' data.
4. **Frontend** (track C) last: v1 pages from section 5, built against the agreed API
   (fake data from section 7's examples until the API is up).
5. Then v2 (pull requests, issues, orgs), then the rest of v3.

**v1 is done when:** someone signs up, creates a repo, `kirch push`es a circuit with a
token; someone else sees it on the website (repo home, commit list, commit with
wiring diff, branches, compare) and `kirch clone`s it; a private repo stays
invisible to people who aren't collaborators.

---

## 11. Open questions

- Website name and domain (`<host>` above).
- Max repo size and max file size for v1 (suggest 100 MB repo, 10 MB file).
- Gravatar in v1, or avatar upload right away?
- Email verification and password reset in v1? (Suggest: reset yes, verification v2.)
- Sign in with GitHub as an extra login option — v2?
