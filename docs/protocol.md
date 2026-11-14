# kirch:// — push / pull protocol (v1)

How `kirch push`, `pull`, `fetch` and `clone` talk to a Kirch server. Our own protocol over **TCP + TLS**, default port **9418**.

```
kirch://<host>[:<port>]/<owner>/<repo>
kirch://localhost/evin/servo-board
```

The website does **not** use this; it uses the HTTP API (port 8080).

---

## Frames

Every message is one frame:

```
+----------------+--------+----------------------+
| length uint32  | type   | payload              |
| big-endian     | 1 byte | length - 1 bytes     |
+----------------+--------+----------------------+
```

- `length` counts the type byte + payload.
- Max frame: **64 MiB** (one object). Bigger → connection closed with `ERR`.
- Payload is **JSON** for every type except `OBJ`.

| Type | Byte | Sent by | Payload |
|---|---|---|---|
| `HELLO` | `1` | client | `{"version":1,"repo":"owner/name","op":"fetch"\|"push"}` |
| `REFS` | `2` | server | `{"head":"main","branches":{"main":"<id>",…}}` |
| `WANT` | `3` | client | `{"wants":["<id>",…],"haves":["<id>",…]}` |
| `OBJ` | `4` | both | 1 byte object type (`b` blob, `t` tree, `c` commit) + zlib-compressed content |
| `DONE` | `5` | both | `{"count":<objects sent>}` |
| `UPDATE` | `6` | client | `{"branch":"main","old":"<id or empty>","new":"<id>"}` |
| `OK` | `7` | server | `{}` |
| `ERR` | `8` | server | `{"code":"…","message":"…"}` |

Ids are 40-char lowercase hex SHA-1, same as git. The receiver re-hashes every `OBJ`; the id is never trusted from the sender.

---

## Fetch (also used by pull and clone)

```
client                              server
  HELLO {op:"fetch"}        ──►
                            ◄──     REFS {head, branches}
  WANT {wants, haves}       ──►
                            ◄──     OBJ … OBJ
                            ◄──     DONE {count}
  (close)
```

- `wants` = remote branch tips the client doesn't have.
- `haves` = commit tips the client already has (its branches + remote-tracking branches).
- Server sends every object reachable from `wants` that is **not** reachable from a `have` it knows. Order: an object always comes after everything it points to (blobs → trees → commits), so an interrupted fetch never leaves a commit without its files.
- Empty `wants` → server replies `DONE {count:0}` right away.
- Client checks every wanted tip is complete before moving `origin/<branch>`.

## Push

```
client                              server
  HELLO {op:"push"}         ──►
                            ◄──     REFS {head, branches}
  OBJ … OBJ                 ──►
  DONE {count}              ──►
  UPDATE {branch, old, new} ──►
                            ◄──     OK   or   ERR
  (close)
```

- Client sends objects reachable from `new` that aren't reachable from any server tip it has.
- Server, before moving the branch:
  1. checks `new` and everything below it is stored (no missing objects),
  2. checks fast-forward: `old` must be an ancestor of `new` (or the branch is new),
  3. moves the branch only if it still points at `old` (someone else pushed first → `ERR stale`).
- After `OK` the server indexes the repo into Postgres.
- Pushing to a repo that doesn't exist yet creates it (v1, no logins).

## Errors

| `code` | Meaning |
|---|---|
| `bad_request` | Malformed frame, unknown type, bad JSON, bad repo name |
| `version` | Client speaks a version the server doesn't |
| `not_found` | Fetch from a repo that doesn't exist |
| `missing_objects` | Push: `new` refers to objects that were never sent |
| `non_fast_forward` | Push: the server branch has commits the client doesn't (pull first) |
| `stale` | Push: the branch moved while you were pushing (pull, then push again) |
| `internal` | Server-side failure |

## Limits and timeouts

- Repo name: `owner/name`, each part `[a-z0-9][a-z0-9._-]{0,62}` (lowercase). No `..`, no slashes inside a part.
- Idle timeout: 60 s without a frame → connection closed.
- Max frame 64 MiB.

## TLS

- Server: `KIRCH_TLS_CERT` + `KIRCH_TLS_KEY`. If unset, it makes a self-signed certificate once and saves it in `<data>/tls/` (local dev).
- Client trusts the system's certificate roots, plus the file in `KIRCH_CA_FILE` if set (point it at the dev server's `cert.pem`).

## Not in v1

Logins / tokens, delete-branch over push, force push, resume of interrupted transfers, delta compression.
