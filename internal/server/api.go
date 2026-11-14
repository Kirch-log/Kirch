package server

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"mime"
	"net/http"
	"path"
	"sort"
	"strconv"
	"strings"

	"github.com/Kirch-log/kirch/internal/db"
	"github.com/Kirch-log/kirch/internal/diff"
	"github.com/Kirch-log/kirch/internal/objects"
	"github.com/Kirch-log/kirch/internal/proto"
)

// Read-only JSON API for the website. Lists come from Postgres; diffs and
// file contents are read from the repository folder by the engine.
//
//	GET /healthz
//	GET /api/repos
//	GET /api/repos/{owner}/{name}
//	GET /api/repos/{owner}/{name}/branches
//	GET /api/repos/{owner}/{name}/commits?branch=&limit=&offset=
//	GET /api/repos/{owner}/{name}/commits/{id}
//	GET /api/repos/{owner}/{name}/commits/{id}/diff
//	GET /api/repos/{owner}/{name}/commits/{id}/files
//	GET /api/repos/{owner}/{name}/commits/{id}/raw/{path...}

const (
	defaultLimit = 30
	maxLimit     = 100
	// maxPatch caps one file's patch text in a diff response.
	maxPatch = 512 << 10
)

// Handler is the HTTP API.
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", s.health)
	mux.HandleFunc("GET /api/repos", s.listRepos)
	mux.HandleFunc("GET /api/repos/{owner}/{name}", s.getRepo)
	mux.HandleFunc("GET /api/repos/{owner}/{name}/branches", s.listBranches)
	mux.HandleFunc("GET /api/repos/{owner}/{name}/commits", s.listCommits)
	mux.HandleFunc("GET /api/repos/{owner}/{name}/commits/{id}", s.getCommit)
	mux.HandleFunc("GET /api/repos/{owner}/{name}/commits/{id}/diff", s.commitDiff)
	mux.HandleFunc("GET /api/repos/{owner}/{name}/commits/{id}/files", s.commitFiles)
	mux.HandleFunc("GET /api/repos/{owner}/{name}/commits/{id}/raw/{path...}", s.rawFile)
	mux.HandleFunc("GET /", func(w http.ResponseWriter, r *http.Request) {
		apiError(w, http.StatusNotFound, "no such API endpoint")
	})
	return s.cors(mux)
}

func (s *Server) cors(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h := w.Header()
		h.Set("X-Content-Type-Options", "nosniff")
		if s.CORSOrigin != "" {
			h.Set("Access-Control-Allow-Origin", s.CORSOrigin)
			h.Set("Vary", "Origin")
		}
		if r.Method == http.MethodOptions {
			h.Set("Access-Control-Allow-Methods", "GET, OPTIONS")
			h.Set("Access-Control-Allow-Headers", "Content-Type")
			h.Set("Access-Control-Max-Age", "3600")
			w.WriteHeader(http.StatusNoContent)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(code)
	enc := json.NewEncoder(w)
	enc.SetEscapeHTML(false)
	enc.Encode(v)
}

func apiError(w http.ResponseWriter, code int, msg string) {
	writeJSON(w, code, map[string]string{"error": msg})
}

// fail answers with err: not found / ambiguous as 404 / 400, anything else
// as a logged 500 that doesn't leak details.
func (s *Server) fail(w http.ResponseWriter, r *http.Request, err error) {
	switch {
	case errors.Is(err, db.ErrNotFound), errors.Is(err, objects.ErrNotFound):
		apiError(w, http.StatusNotFound, "not found")
	case errors.Is(err, db.ErrAmbiguous):
		apiError(w, http.StatusBadRequest, err.Error())
	default:
		s.logf("%s %s: %v", r.Method, r.URL.Path, err)
		apiError(w, http.StatusInternalServerError, "server error")
	}
}

func (s *Server) health(w http.ResponseWriter, r *http.Request) {
	if err := s.DB.Pool.Ping(r.Context()); err != nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"status": "database unavailable"})
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

func (s *Server) listRepos(w http.ResponseWriter, r *http.Request) {
	repos, err := s.DB.ListRepos(r.Context())
	if err != nil {
		s.fail(w, r, err)
		return
	}
	if repos == nil {
		repos = []db.Repo{}
	}
	writeJSON(w, http.StatusOK, map[string]any{"repos": repos})
}

// repoFrom looks up {owner}/{name}. On failure it has already answered.
func (s *Server) repoFrom(w http.ResponseWriter, r *http.Request) (db.Repo, bool) {
	owner, name := r.PathValue("owner"), r.PathValue("name")
	if proto.CheckRepoName(owner) != nil || proto.CheckRepoName(name) != nil {
		apiError(w, http.StatusNotFound, "not found")
		return db.Repo{}, false
	}
	repo, err := s.DB.GetRepo(r.Context(), owner, name)
	if err != nil {
		s.fail(w, r, err)
		return db.Repo{}, false
	}
	return repo, true
}

func (s *Server) getRepo(w http.ResponseWriter, r *http.Request) {
	repo, ok := s.repoFrom(w, r)
	if !ok {
		return
	}
	branches, err := s.DB.Branches(r.Context(), repo.ID)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	names := make([]string, len(branches))
	for i, b := range branches {
		names[i] = b.Name
	}
	writeJSON(w, http.StatusOK, struct {
		db.Repo
		BranchNames []string `json:"branches"`
	}{repo, names})
}

func (s *Server) listBranches(w http.ResponseWriter, r *http.Request) {
	repo, ok := s.repoFrom(w, r)
	if !ok {
		return
	}
	branches, err := s.DB.Branches(r.Context(), repo.ID)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	if branches == nil {
		branches = []db.Branch{}
	}
	writeJSON(w, http.StatusOK, map[string]any{"head": repo.HeadBranch, "branches": branches})
}

func intParam(r *http.Request, key string, def, min, max int) (int, error) {
	v := r.URL.Query().Get(key)
	if v == "" {
		return def, nil
	}
	n, err := strconv.Atoi(v)
	if err != nil || n < min || n > max {
		return 0, fmt.Errorf("%s must be a number from %d to %d", key, min, max)
	}
	return n, nil
}

func (s *Server) listCommits(w http.ResponseWriter, r *http.Request) {
	repo, ok := s.repoFrom(w, r)
	if !ok {
		return
	}
	limit, err := intParam(r, "limit", defaultLimit, 1, maxLimit)
	if err != nil {
		apiError(w, http.StatusBadRequest, err.Error())
		return
	}
	offset, err := intParam(r, "offset", 0, 0, 1<<30)
	if err != nil {
		apiError(w, http.StatusBadRequest, err.Error())
		return
	}
	branch := r.URL.Query().Get("branch")
	if branch == "" {
		branch = repo.HeadBranch
	}
	tip, err := s.DB.BranchTip(r.Context(), repo.ID, branch)
	if errors.Is(err, db.ErrNotFound) {
		apiError(w, http.StatusNotFound, fmt.Sprintf("no branch %q", branch))
		return
	}
	if err != nil {
		s.fail(w, r, err)
		return
	}
	// Ask for one extra to know whether there's a next page.
	commits, err := s.DB.History(r.Context(), repo.ID, tip, limit+1, offset)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	var next *int
	if len(commits) > limit {
		commits = commits[:limit]
		n := offset + limit
		next = &n
	}
	if commits == nil {
		commits = []db.Commit{}
	}
	writeJSON(w, http.StatusOK, map[string]any{"branch": branch, "commits": commits, "next_offset": next})
}

// commitFrom looks up {id} in the repo. On failure it has already answered.
func (s *Server) commitFrom(w http.ResponseWriter, r *http.Request) (db.Repo, db.Commit, bool) {
	repo, ok := s.repoFrom(w, r)
	if !ok {
		return repo, db.Commit{}, false
	}
	c, err := s.DB.GetCommit(r.Context(), repo.ID, r.PathValue("id"))
	if err != nil {
		s.fail(w, r, err)
		return repo, c, false
	}
	return repo, c, true
}

// FileChange is one file in a commit's diff.
type FileChange struct {
	Path      string         `json:"path"`
	Status    string         `json:"status"` // added, deleted, modified, mode
	OldID     string         `json:"old_id,omitempty"`
	NewID     string         `json:"new_id,omitempty"`
	OldMode   string         `json:"old_mode,omitempty"`
	NewMode   string         `json:"new_mode,omitempty"`
	Binary    bool           `json:"binary"`
	Wiring    []WiringChange `json:"wiring,omitempty"`
	Patch     string         `json:"patch,omitempty"`
	Truncated bool           `json:"truncated,omitempty"`
}

// WiringChange is one netlist.json change, with the text kirch diff shows.
type WiringChange struct {
	Kind   string   `json:"kind"`
	Text   string   `json:"text"`
	Net    string   `json:"net,omitempty"`
	OldNet string   `json:"old_net,omitempty"`
	Pin    string   `json:"pin,omitempty"`
	OldPin string   `json:"old_pin,omitempty"`
	Pins   []string `json:"pins,omitempty"`
}

func status(f diff.File) string {
	switch {
	case f.OldID == "":
		return "added"
	case f.NewID == "":
		return "deleted"
	case f.OldID == f.NewID:
		return "mode"
	}
	return "modified"
}

func (s *Server) changes(owner, name, id string, withContent bool) ([]FileChange, error) {
	rp, err := s.Repos.Open(owner, name)
	if err != nil {
		return nil, err
	}
	files, err := rp.DiffParent(id)
	if err != nil {
		return nil, err
	}
	sort.Slice(files, func(i, j int) bool { return files[i].Path < files[j].Path })
	out := make([]FileChange, 0, len(files))
	for _, f := range files {
		fc := FileChange{Path: f.Path, Status: status(f), OldID: f.OldID, NewID: f.NewID, OldMode: f.OldMode, NewMode: f.NewMode,
			Binary: diff.IsBinary(f.Old) || diff.IsBinary(f.New)}
		if withContent {
			if path.Base(f.Path) == "netlist.json" && !fc.Binary {
				if ws, err := diff.Wiring(f.Old, f.New); err == nil {
					for _, c := range ws {
						fc.Wiring = append(fc.Wiring, WiringChange{Kind: string(c.Kind), Text: c.String(), Net: c.Net,
							OldNet: c.OldNet, Pin: c.Pin, OldPin: c.OldPin, Pins: c.Pins})
					}
				}
			}
			var buf bytes.Buffer
			diff.WritePatch(&buf, f)
			if buf.Len() > maxPatch {
				fc.Patch, fc.Truncated = string(buf.Bytes()[:maxPatch]), true
			} else {
				fc.Patch = buf.String()
			}
		}
		out = append(out, fc)
	}
	return out, nil
}

func (s *Server) getCommit(w http.ResponseWriter, r *http.Request) {
	repo, c, ok := s.commitFrom(w, r)
	if !ok {
		return
	}
	files, err := s.changes(repo.Owner, repo.Name, c.ID, false)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, struct {
		db.Commit
		Files []FileChange `json:"files"`
	}{c, files})
}

func (s *Server) commitDiff(w http.ResponseWriter, r *http.Request) {
	repo, c, ok := s.commitFrom(w, r)
	if !ok {
		return
	}
	files, err := s.changes(repo.Owner, repo.Name, c.ID, true)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	var parent *string
	if len(c.Parents) > 0 {
		parent = &c.Parents[0]
	}
	writeJSON(w, http.StatusOK, map[string]any{"commit": c.ID, "parent": parent, "files": files})
}

// TreeFile is one file in a commit's snapshot.
type TreeFile struct {
	Path string `json:"path"`
	Mode string `json:"mode"`
	ID   string `json:"id"`
}

func (s *Server) commitFiles(w http.ResponseWriter, r *http.Request) {
	repo, c, ok := s.commitFrom(w, r)
	if !ok {
		return
	}
	rp, err := s.Repos.Open(repo.Owner, repo.Name)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	files, err := rp.TreeFiles(c.Tree)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	out := make([]TreeFile, 0, len(files))
	for p, e := range files {
		out = append(out, TreeFile{Path: p, Mode: e.Mode, ID: e.ID})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Path < out[j].Path })
	writeJSON(w, http.StatusOK, map[string]any{"commit": c.ID, "files": out})
}

func (s *Server) rawFile(w http.ResponseWriter, r *http.Request) {
	repo, c, ok := s.commitFrom(w, r)
	if !ok {
		return
	}
	rp, err := s.Repos.Open(repo.Owner, repo.Name)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	files, err := rp.TreeFiles(c.Tree)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	p := strings.Trim(r.PathValue("path"), "/")
	e, found := files[p]
	if !found {
		apiError(w, http.StatusNotFound, "no such file in this commit")
		return
	}
	_, data, err := rp.Objects.Read(e.ID)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	h := w.Header()
	// Never let a stored file run as a web page on our origin.
	h.Set("Content-Security-Policy", "sandbox; default-src 'none'")
	switch {
	case diff.IsBinary(data):
		h.Set("Content-Type", "application/octet-stream")
		h.Set("Content-Disposition", mime.FormatMediaType("attachment", map[string]string{"filename": path.Base(p)}))
	case path.Ext(p) == ".json":
		h.Set("Content-Type", "application/json; charset=utf-8")
	default:
		h.Set("Content-Type", "text/plain; charset=utf-8")
	}
	h.Set("Cache-Control", "public, max-age=31536000, immutable") // a commit's files never change
	w.Write(data)
}
