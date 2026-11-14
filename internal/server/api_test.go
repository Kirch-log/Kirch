package server

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Kirch-log/kirch/internal/db/dbtest"
	"github.com/Kirch-log/kirch/internal/indexer"
	"github.com/Kirch-log/kirch/internal/repo"
)

func TestMain(m *testing.M) { dbtest.Main(m) }

type apiFixture struct {
	t    *testing.T
	s    *Server
	http *httptest.Server
	r    *repo.Repo // evin/servo-board on the server's disk
	ids  []string   // commits, oldest first
}

func newAPI(t *testing.T) *apiFixture {
	t.Helper()
	s := &Server{Repos: Repos{DataDir: t.TempDir()}, DB: dbtest.New(t), CORSOrigin: "http://localhost:3000"}
	if err := s.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	r, err := repo.Init(s.Repos.Path("evin", "servo-board"))
	if err != nil {
		t.Fatal(err)
	}
	f := &apiFixture{t: t, s: s, r: r}
	f.http = httptest.NewServer(s.Handler())
	t.Cleanup(f.http.Close)
	return f
}

func (f *apiFixture) commit(files map[string]string, msg string) string {
	f.t.Helper()
	for p, c := range files {
		write(f.t, f.r, p, c)
	}
	id := commit(f.t, f.r, msg)
	f.ids = append(f.ids, id)
	return id
}

func (f *apiFixture) index() {
	f.t.Helper()
	if _, err := indexer.IndexRepo(context.Background(), f.s.DB, f.s.Repos, "evin", "servo-board"); err != nil {
		f.t.Fatal(err)
	}
}

func (f *apiFixture) get(path string, wantCode int, into any) http.Header {
	f.t.Helper()
	resp, err := http.Get(f.http.URL + path)
	if err != nil {
		f.t.Fatal(err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != wantCode {
		f.t.Fatalf("GET %s: %d %s, want %d", path, resp.StatusCode, body, wantCode)
	}
	if into != nil {
		if err := json.Unmarshal(body, into); err != nil {
			f.t.Fatalf("GET %s: %v in %s", path, err, body)
		}
	}
	return resp.Header
}

func (f *apiFixture) sample() {
	f.commit(map[string]string{"netlist.json": netlist("U1.GPIO9"), "firmware/main.cpp": "void setup() {}\n"}, "Servo on GPIO9")
	f.commit(map[string]string{"netlist.json": netlist("U1.GPIO10")}, "Move servo to GPIO10")
	f.commit(map[string]string{"photo.bin": "\x00\x01\x02 binary"}, "Add photo\n\nOf the bench.")
	f.index()
}

type commitsResp struct {
	Branch  string `json:"branch"`
	Commits []struct {
		ID      string   `json:"id"`
		Summary string   `json:"summary"`
		Message string   `json:"message"`
		Parents []string `json:"parents"`
		Author  struct {
			Name string `json:"name"`
		} `json:"author"`
	} `json:"commits"`
	Next *int `json:"next_offset"`
}

func TestAPIReposAndBranches(t *testing.T) {
	f := newAPI(t)
	f.sample()
	var list struct {
		Repos []struct {
			Owner, Name string
			HeadBranch  string `json:"head_branch"`
			Commits     int    `json:"commit_count"`
			LastCommit  struct {
				Summary string
			} `json:"last_commit"`
		}
	}
	f.get("/api/repos", 200, &list)
	if len(list.Repos) != 1 || list.Repos[0].Name != "servo-board" || list.Repos[0].Commits != 3 || list.Repos[0].LastCommit.Summary != "Add photo" {
		t.Fatalf("repos: %+v", list)
	}
	var one struct {
		Owner    string
		Branches []string
	}
	f.get("/api/repos/evin/servo-board", 200, &one)
	if one.Owner != "evin" || len(one.Branches) != 1 || one.Branches[0] != "main" {
		t.Fatalf("repo: %+v", one)
	}
	var bs struct {
		Head     string
		Branches []struct {
			Name   string
			Commit struct{ ID string }
		}
	}
	f.get("/api/repos/evin/servo-board/branches", 200, &bs)
	if bs.Head != "main" || len(bs.Branches) != 1 || bs.Branches[0].Commit.ID != f.ids[2] {
		t.Fatalf("branches: %+v", bs)
	}
	f.get("/api/repos/evin/nope", 404, nil)
	f.get("/api/repos/EVIL/x", 404, nil)
	f.get("/api/repos/evin/a..b", 404, nil)
	f.get("/api/nothing", 404, nil)
}

func TestAPICommitsPaging(t *testing.T) {
	f := newAPI(t)
	f.sample()
	var page commitsResp
	f.get("/api/repos/evin/servo-board/commits?limit=2", 200, &page)
	if page.Branch != "main" || len(page.Commits) != 2 || page.Next == nil || *page.Next != 2 {
		t.Fatalf("page 1: %+v", page)
	}
	if page.Commits[0].ID != f.ids[2] || page.Commits[1].ID != f.ids[1] {
		t.Fatal("page 1 not newest first")
	}
	if page.Commits[0].Summary != "Add photo" || page.Commits[0].Message != "Add photo\n\nOf the bench.\n" || page.Commits[0].Author.Name != "Test" {
		t.Fatalf("commit fields: %+v", page.Commits[0])
	}
	var page2 commitsResp
	f.get("/api/repos/evin/servo-board/commits?limit=2&offset=2", 200, &page2)
	if len(page2.Commits) != 1 || page2.Commits[0].ID != f.ids[0] || page2.Next != nil || len(page2.Commits[0].Parents) != 0 {
		t.Fatalf("page 2: %+v", page2)
	}
	f.get("/api/repos/evin/servo-board/commits?limit=0", 400, nil)
	f.get("/api/repos/evin/servo-board/commits?limit=500", 400, nil)
	f.get("/api/repos/evin/servo-board/commits?offset=-1", 400, nil)
	f.get("/api/repos/evin/servo-board/commits?branch=nope", 404, nil)
}

type diffResp struct {
	Commit string  `json:"commit"`
	Parent *string `json:"parent"`
	Files  []struct {
		Path   string
		Status string
		Binary bool
		Patch  string
		Wiring []struct {
			Kind, Text, Pin, OldPin string
		}
	}
}

func TestAPIDiff(t *testing.T) {
	f := newAPI(t)
	f.sample()
	var d diffResp
	f.get("/api/repos/evin/servo-board/commits/"+f.ids[1][:8]+"/diff", 200, &d)
	if d.Commit != f.ids[1] || d.Parent == nil || *d.Parent != f.ids[0] || len(d.Files) != 1 {
		t.Fatalf("diff: %+v", d)
	}
	file := d.Files[0]
	if file.Path != "netlist.json" || file.Status != "modified" || len(file.Wiring) != 1 ||
		file.Wiring[0].Text != "SERVO1.SIG: U1.GPIO9 → U1.GPIO10" || file.Wiring[0].Kind != "pin-swapped" {
		t.Fatalf("wiring: %+v", file)
	}
	if !strings.Contains(file.Patch, "-") || !strings.HasPrefix(file.Patch, "diff --git a/netlist.json b/netlist.json") {
		t.Fatalf("patch: %q", file.Patch)
	}

	// First commit: everything added, no parent.
	var first diffResp
	f.get("/api/repos/evin/servo-board/commits/"+f.ids[0]+"/diff", 200, &first)
	if first.Parent != nil || len(first.Files) != 2 || first.Files[0].Status != "added" || first.Files[0].Path != "firmware/main.cpp" {
		t.Fatalf("first diff: %+v", first)
	}
	// Binary file: no patch body.
	var bin diffResp
	f.get("/api/repos/evin/servo-board/commits/"+f.ids[2]+"/diff", 200, &bin)
	if len(bin.Files) != 1 || !bin.Files[0].Binary || !strings.Contains(bin.Files[0].Patch, "Binary files") {
		t.Fatalf("binary diff: %+v", bin)
	}

	// Commit detail lists changed files without contents.
	var detail struct {
		ID    string
		Files []struct{ Path, Status, Patch string }
	}
	f.get("/api/repos/evin/servo-board/commits/"+f.ids[1], 200, &detail)
	if detail.ID != f.ids[1] || len(detail.Files) != 1 || detail.Files[0].Patch != "" {
		t.Fatalf("detail: %+v", detail)
	}
	f.get("/api/repos/evin/servo-board/commits/"+strings.Repeat("0", 40), 404, nil)
	f.get("/api/repos/evin/servo-board/commits/xyz/diff", 404, nil)
}

func TestAPIFilesAndRaw(t *testing.T) {
	f := newAPI(t)
	f.sample()
	var files struct {
		Files []struct{ Path, Mode, ID string }
	}
	f.get("/api/repos/evin/servo-board/commits/"+f.ids[2]+"/files", 200, &files)
	var paths []string
	for _, x := range files.Files {
		paths = append(paths, x.Path)
	}
	if strings.Join(paths, ",") != "firmware/main.cpp,netlist.json,photo.bin" {
		t.Fatalf("files: %v", paths)
	}

	base := "/api/repos/evin/servo-board/commits/" + f.ids[1] + "/raw/"
	resp, err := http.Get(f.http.URL + base + "netlist.json")
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != 200 || !strings.Contains(string(body), "U1.GPIO10") || !strings.HasPrefix(resp.Header.Get("Content-Type"), "application/json") {
		t.Fatalf("raw netlist: %d %s %q", resp.StatusCode, resp.Header.Get("Content-Type"), body)
	}
	if !strings.Contains(resp.Header.Get("Content-Security-Policy"), "sandbox") {
		t.Error("raw files must be sandboxed")
	}
	h := f.get(base+"firmware/main.cpp", 200, nil)
	if !strings.HasPrefix(h.Get("Content-Type"), "text/plain") {
		t.Errorf("cpp content type %q", h.Get("Content-Type"))
	}
	h = f.get("/api/repos/evin/servo-board/commits/"+f.ids[2]+"/raw/photo.bin", 200, nil)
	if h.Get("Content-Type") != "application/octet-stream" || !strings.Contains(h.Get("Content-Disposition"), "attachment") {
		t.Errorf("binary headers: %v", h)
	}
	f.get(base+"photo.bin", 404, nil) // not in that commit yet
	f.get(base+"../../../etc/passwd", 404, nil)
	f.get(base+"firmware", 404, nil) // folders aren't files
}

func TestAPICORSAndHealth(t *testing.T) {
	f := newAPI(t)
	h := f.get("/healthz", 200, nil)
	if h.Get("Access-Control-Allow-Origin") != "http://localhost:3000" {
		t.Errorf("CORS origin %q", h.Get("Access-Control-Allow-Origin"))
	}
	req, _ := http.NewRequest(http.MethodOptions, f.http.URL+"/api/repos", nil)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != 204 || !strings.Contains(resp.Header.Get("Access-Control-Allow-Methods"), "GET") {
		t.Fatalf("preflight: %d %v", resp.StatusCode, resp.Header)
	}
	var empty struct{ Repos []any }
	f.get("/api/repos", 200, &empty)
	if empty.Repos == nil {
		t.Error("empty repo list should be [] not null")
	}
	req, _ = http.NewRequest(http.MethodPost, f.http.URL+"/api/repos", nil)
	resp, _ = http.DefaultClient.Do(req)
	resp.Body.Close()
	if resp.StatusCode != http.StatusMethodNotAllowed {
		t.Errorf("POST: %d, want 405", resp.StatusCode)
	}
}

// TestPushShowsUpInAPI is the whole server: a push over kirch:// is
// indexed right away and readable through the HTTP API.
func TestPushShowsUpInAPI(t *testing.T) {
	f := newAPI(t)
	certPEM, keyPEM, err := SelfSigned(nil)
	if err != nil {
		t.Fatal(err)
	}
	ca := filepath.Join(t.TempDir(), "ca.pem")
	os.WriteFile(ca, certPEM, 0o644)
	t.Setenv("KIRCH_CA_FILE", ca)
	cert, _ := tls.X509KeyPair(certPEM, keyPEM)
	ln, err := tls.Listen("tcp", "127.0.0.1:0", &tls.Config{Certificates: []tls.Certificate{cert}})
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan struct{})
	go func() { f.s.ServeTCP(ln); close(done) }()
	t.Cleanup(func() { ln.Close(); <-done })

	a := newRepo(t)
	write(t, a, "netlist.json", netlist("U1.GPIO9"))
	c1 := commit(t, a, "Servo on GPIO9")
	a.AddRemote("origin", "kirch://"+ln.Addr().String()+"/hb/probe-rig")
	if _, err := a.Push("origin", ""); err != nil {
		t.Fatal(err)
	}
	var page commitsResp
	f.get("/api/repos/hb/probe-rig/commits", 200, &page)
	if len(page.Commits) != 1 || page.Commits[0].ID != c1 {
		t.Fatalf("after push: %+v", page)
	}
	write(t, a, "netlist.json", netlist("U1.GPIO12"))
	c2 := commit(t, a, "Move to GPIO12")
	if _, err := a.Push("origin", ""); err != nil {
		t.Fatal(err)
	}
	f.get("/api/repos/hb/probe-rig/commits", 200, &page)
	if len(page.Commits) != 2 || page.Commits[0].ID != c2 {
		t.Fatalf("after second push: %+v", page)
	}
	var d diffResp
	f.get("/api/repos/hb/probe-rig/commits/"+c2+"/diff", 200, &d)
	if len(d.Files) != 1 || len(d.Files[0].Wiring) != 1 || d.Files[0].Wiring[0].Text != "SERVO1.SIG: U1.GPIO9 → U1.GPIO12" {
		t.Fatalf("diff: %+v", d)
	}
}
