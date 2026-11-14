// Package server is the push / pull side of a Kirch server: the endpoints
// kirch clone, fetch, pull and push talk to (internal/protocol).
//
// It is an http.Handler, so the main platform server (track B) mounts it
// next to its own API and plugs in its logins and database through the
// Authorize and OnPush hooks. `kirch serve` runs it on its own for local use.
//
// Repositories live on disk at Root/<owner>/<repo>/.kirch.
package server

import (
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/Kirch-log/kirch/internal/objects"
	"github.com/Kirch-log/kirch/internal/protocol"
	"github.com/Kirch-log/kirch/internal/refs"
	"github.com/Kirch-log/kirch/internal/repo"
)

// Access is what a request wants to do.
type Access int

const (
	Read  Access = iota // clone, fetch, pull
	Write               // push
)

// PushEvent describes a branch a push moved (for the database and the
// activity feed, docs/platform.md section 8).
type PushEvent struct {
	Owner, Repo string
	Branch      string
	Before      string // "" = branch created
	After       string
	Objects     int // objects received
	User        string
}

// HTTPError lets Authorize choose the status code (401 log in, 403 not
// allowed, 404 hide that a private repo exists).
type HTTPError struct {
	Status  int
	Message string
}

func (e *HTTPError) Error() string { return e.Message }

// Server serves repositories under Root.
type Server struct {
	Root string

	// AutoCreate makes a repository on its first push (kirch serve). The
	// platform leaves it off: repos are created on the website.
	AutoCreate bool

	// MaxPushBytes limits the unpacked size of one push (0 = 100 MB).
	MaxPushBytes int64

	// Authorize decides whether the request may read or write the repo and
	// returns the user name for PushEvent. nil allows everything (kirch
	// serve on your own computer).
	Authorize func(r *http.Request, owner, repo string, access Access) (user string, err error)

	// OnPush runs after a push moved a branch.
	OnPush func(PushEvent)

	// Log gets one line per request that changes something (nil = silent).
	Log *log.Logger
}

// Handler routes:
//
//	GET  /{owner}/{repo}/info/refs
//	POST /{owner}/{repo}/kirch-fetch
//	POST /{owner}/{repo}/kirch-push?branch=&old=&new=
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /{owner}/{repo}"+protocol.RefsPath, s.refs)
	mux.HandleFunc("POST /{owner}/{repo}"+protocol.FetchPath, s.fetch)
	mux.HandleFunc("POST /{owner}/{repo}"+protocol.PushPath, s.push)
	return mux
}

// Names: letters, digits, '-', '_', '.', not starting with '.' and no "..",
// so a name can never point outside Root.
var nameRe = regexp.MustCompile(`^[A-Za-z0-9_-][A-Za-z0-9._-]{0,99}$`)

func validName(s string) bool { return nameRe.MatchString(s) && !strings.Contains(s, "..") }

func (s *Server) repoDir(owner, name string) string {
	return filepath.Join(s.Root, owner, strings.TrimSuffix(name, ".kirch"))
}

// open finds the repository and checks access; it writes the error itself.
func (s *Server) open(w http.ResponseWriter, r *http.Request, access Access) (*repo.Repo, string, string, string, bool) {
	owner, name := r.PathValue("owner"), r.PathValue("repo")
	if !validName(owner) || !validName(name) {
		fail(w, http.StatusNotFound, "", "repository not found")
		return nil, "", "", "", false
	}
	name = strings.TrimSuffix(name, ".kirch")
	user := ""
	if s.Authorize != nil {
		u, err := s.Authorize(r, owner, name, access)
		if err != nil {
			authFail(w, err)
			return nil, "", "", "", false
		}
		user = u
	}
	dir := s.repoDir(owner, name)
	if _, err := os.Stat(filepath.Join(dir, repo.DirName, "HEAD")); err != nil {
		if access == Write && s.AutoCreate {
			rp, err := repo.Init(dir)
			if err != nil && !errors.Is(err, repo.ErrExists) {
				fail(w, http.StatusInternalServerError, "", "can't create repository")
				return nil, "", "", "", false
			}
			if rp == nil {
				rp, _ = repo.Find(dir)
			}
			return rp, owner, name, user, rp != nil
		}
		fail(w, http.StatusNotFound, "", "repository not found")
		return nil, "", "", "", false
	}
	rp, err := repo.Find(dir)
	if err != nil {
		fail(w, http.StatusNotFound, "", "repository not found")
		return nil, "", "", "", false
	}
	return rp, owner, name, user, true
}

func (s *Server) refs(w http.ResponseWriter, r *http.Request) {
	// With AutoCreate, a repository that doesn't exist yet looks empty, so
	// the first push can go ahead (it creates the repository).
	// Only someone allowed to create it (write access) sees that.
	if s.AutoCreate {
		owner, name := r.PathValue("owner"), r.PathValue("repo")
		if validName(owner) && validName(name) {
			if _, err := os.Stat(filepath.Join(s.repoDir(owner, name), repo.DirName, "HEAD")); err != nil {
				if s.Authorize != nil {
					if _, err := s.Authorize(r, owner, strings.TrimSuffix(name, ".kirch"), Write); err != nil {
						authFail(w, err)
						return
					}
				}
				writeJSON(w, http.StatusOK, protocol.Refs{Head: repo.DefaultBranch, Branches: map[string]string{}})
				return
			}
		}
	}
	rp, _, _, _, ok := s.open(w, r, Read)
	if !ok {
		return
	}
	head, _, err := rp.Refs().Head()
	if err != nil {
		fail(w, http.StatusInternalServerError, "", "can't read HEAD")
		return
	}
	branches, err := rp.Refs().Branches()
	if err != nil {
		fail(w, http.StatusInternalServerError, "", "can't read branches")
		return
	}
	writeJSON(w, http.StatusOK, protocol.Refs{Head: head, Branches: branches})
}

func (s *Server) fetch(w http.ResponseWriter, r *http.Request) {
	rp, _, _, _, ok := s.open(w, r, Read)
	if !ok {
		return
	}
	var req protocol.FetchRequest
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20)).Decode(&req); err != nil {
		fail(w, http.StatusBadRequest, "", "bad fetch request")
		return
	}
	if len(req.Want) == 0 {
		fail(w, http.StatusBadRequest, "", "nothing wanted")
		return
	}
	// Only commits a branch points to (or their history) may be fetched.
	branches, err := rp.Refs().Branches()
	if err != nil {
		fail(w, http.StatusInternalServerError, "", "can't read branches")
		return
	}
	tips := map[string]bool{}
	for _, id := range branches {
		tips[id] = true
	}
	for _, id := range req.Want {
		if !objects.ValidID(id) || !tips[id] {
			fail(w, http.StatusBadRequest, "", fmt.Sprintf("%q is not a branch tip here (fetch again)", id))
			return
		}
	}
	ids, err := repo.ObjectsToSend(rp.Objects, req.Want, req.Have)
	if err != nil {
		fail(w, http.StatusInternalServerError, "", "can't read repository")
		return
	}
	w.Header().Set("Content-Type", protocol.BundleType)
	w.WriteHeader(http.StatusOK)
	if err := protocol.WriteBundle(w, rp.Objects, ids); err != nil && s.Log != nil {
		s.Log.Printf("fetch: %v", err)
	}
}

func (s *Server) push(w http.ResponseWriter, r *http.Request) {
	rp, owner, name, user, ok := s.open(w, r, Write)
	if !ok {
		return
	}
	q := r.URL.Query()
	branch, old, new := q.Get("branch"), q.Get("old"), q.Get("new")
	if err := refs.CheckBranchName(branch); err != nil {
		fail(w, http.StatusBadRequest, "", err.Error())
		return
	}
	if !objects.ValidID(new) || old != "" && !objects.ValidID(old) {
		fail(w, http.StatusBadRequest, "", "bad commit id")
		return
	}
	current, err := rp.Refs().Branch(branch)
	if err != nil {
		fail(w, http.StatusInternalServerError, "", "can't read branch")
		return
	}
	if current != old {
		fail(w, http.StatusConflict, protocol.CodeNonFastForward, "the branch has commits you don't have; pull first")
		return
	}

	max := s.MaxPushBytes
	if max == 0 {
		max = 100 << 20
	}
	received, err := protocol.ReadBundle(r.Body, rp.Objects, max)
	if err != nil {
		status := http.StatusBadRequest
		if errors.Is(err, protocol.ErrTooBig) {
			status = http.StatusRequestEntityTooLarge
		}
		fail(w, status, "", err.Error())
		return
	}
	// Everything the new commit needs must be here now.
	if _, err := repo.Closure(rp.Objects, []string{new}); err != nil {
		fail(w, http.StatusBadRequest, "", "push is missing objects: "+err.Error())
		return
	}
	if _, err := rp.ReadCommit(new); err != nil {
		fail(w, http.StatusBadRequest, "", fmt.Sprintf("%s is not a commit", new))
		return
	}
	if old != "" {
		ff, err := rp.IsAncestor(old, new)
		if err != nil || !ff {
			fail(w, http.StatusConflict, protocol.CodeNonFastForward, "not a fast-forward; pull first")
			return
		}
	}
	if err := rp.Refs().UpdateBranch(branch, new, old); err != nil {
		if errors.Is(err, refs.ErrStale) || errors.Is(err, refs.ErrLocked) {
			fail(w, http.StatusConflict, protocol.CodeStale, "someone else pushed at the same time; pull and try again")
			return
		}
		fail(w, http.StatusInternalServerError, "", "can't move branch")
		return
	}
	// First branch in an empty repository becomes its default branch.
	if head, headID, err := rp.Refs().Head(); err == nil && headID == "" && head != branch {
		_ = rp.Refs().SetHead(branch)
	}
	if s.Log != nil {
		s.Log.Printf("push %s/%s %s %s..%s (%d objects) by %q", owner, name, branch, short(old), short(new), len(received), user)
	}
	if s.OnPush != nil {
		s.OnPush(PushEvent{Owner: owner, Repo: name, Branch: branch, Before: old, After: new, Objects: len(received), User: user})
	}
	writeJSON(w, http.StatusOK, protocol.PushResult{Branch: branch, Old: old, New: new, Objects: len(received)})
}

// Repos lists owner/repo names under Root (for kirch serve's start message).
func (s *Server) Repos() []string {
	var out []string
	owners, _ := os.ReadDir(s.Root)
	for _, o := range owners {
		if !o.IsDir() || !validName(o.Name()) {
			continue
		}
		repos, _ := os.ReadDir(filepath.Join(s.Root, o.Name()))
		for _, r := range repos {
			if _, err := os.Stat(filepath.Join(s.Root, o.Name(), r.Name(), repo.DirName, "HEAD")); err == nil {
				out = append(out, o.Name()+"/"+r.Name())
			}
		}
	}
	sort.Strings(out)
	return out
}

func short(id string) string {
	if len(id) < 7 {
		return "(new)"
	}
	return id[:7]
}

func authFail(w http.ResponseWriter, err error) {
	var he *HTTPError
	if errors.As(err, &he) {
		if he.Status == http.StatusUnauthorized {
			w.Header().Set("WWW-Authenticate", `Basic realm="kirch"`)
		}
		fail(w, he.Status, "", he.Message)
		return
	}
	fail(w, http.StatusForbidden, "", err.Error())
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func fail(w http.ResponseWriter, status int, code, msg string) {
	writeJSON(w, status, protocol.Error{Error: msg, Code: code})
}
