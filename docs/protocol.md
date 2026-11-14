# Push / pull over the network

How `kirch clone / fetch / pull / push` talk to a Kirch server.
Code: `internal/protocol` (wire format), `internal/server` (server side),
`internal/repo/remote_http.go` (client side).

## Try it on one computer

```
kirch serve --root kirch-server                         # terminal 1
kirch remote add origin http://localhost:8080/ana/servo-board
kirch push                                              # creates ana/servo-board
kirch clone http://localhost:8080/ana/servo-board       # anywhere else
```

`kirch serve` has no logins — only for your own computer or a network you trust.

## Endpoints (under the clone URL `https://<host>/{owner}/{repo}`)

| Request | Body | Answer |
|---|---|---|
| `GET /{o}/{r}/info/refs` | — | `{"head":"main","branches":{"main":"<id>"}}` |
| `POST /{o}/{r}/kirch-fetch` | `{"want":[ids],"have":[ids]}` | bundle |
| `POST /{o}/{r}/kirch-push?branch=B&old=O&new=N` | bundle | `{"branch","old","new","objects"}` |

Errors are JSON `{"error": "...", "code": "..."}`. Codes the client acts on:
`non_fast_forward` (409, pull first) and `stale` (409, someone pushed at the
same moment). 401 = log in, 403 = not allowed, 404 = not found or private,
413 = push too big.

**Bundle** = gzip of `KIRCH-BUNDLE 1\n`, then for each object
`<type> <size> <id>\n<content>`, then `end\n`. Objects come blobs → trees →
commits. The receiver re-hashes every object and rejects any whose id doesn't
match.

**Fetch** sends only what you don't have: the client lists every commit it
already has (`have`), and the server leaves out everything in their history.

**Push** is safe against races and broken uploads:
1. client checks it's a fast-forward of the server's branch (else "pull first")
2. client sends only objects the server doesn't have (it skips the history of every server branch it knows)
3. server checks the new commit's whole history is present, that it's a fast-forward, then moves the branch with compare-and-swap — of two pushes at the same moment exactly one wins, the other gets `stale`
4. the first branch pushed to an empty repo becomes its default branch

## Logins (client)

Tokens are never saved in the repo. Set them in the environment:

```
KIRCH_USER=ana  KIRCH_TOKEN=<personal access token>
```

They're sent as HTTP basic auth, like GitHub. `kirch remote add` refuses a URL
with a token in it.

## Mounting it in the platform server (Evin)

```go
pp := &server.Server{
    Root: "/data/repos",          // repos at /data/repos/{owner}/{repo}/.kirch
    MaxPushBytes: 100 << 20,      // docs/platform.md open question: 100 MB
    Authorize: func(r *http.Request, owner, repo string, a server.Access) (string, error) {
        // check r.BasicAuth() token → user; public repo: Read for anyone;
        // Write needs a collaborator. Return &server.HTTPError{Status: 404, ...}
        // to hide private repos.
        return user, nil
    },
    OnPush: func(e server.PushEvent) {
        // write push_events, refs, commits (+ stats) — docs/platform.md section 8
    },
}
h := pp.Handler()   // chi: r.Handle("/{owner}/{repo}/info/refs", h), …/kirch-fetch, …/kirch-push
```

Repos are created by the website (`repo.Init(Root/owner/name)`), so leave
`AutoCreate` off on the platform.
