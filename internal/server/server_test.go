package server

import (
	"bytes"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Kirch-log/kirch/internal/objects"
	"github.com/Kirch-log/kirch/internal/protocol"
	"github.com/Kirch-log/kirch/internal/repo"
)

var author = objects.Signature{Name: "T", Email: "t@example.com", When: time.Unix(1794621600, 0).UTC()}

const netlist = `{"format":"kirch.netlist/1","nets":{"SIG":["U1.GPIO9","SERVO1.SIG"]}}`

func newServer(t *testing.T, s *Server) (*httptest.Server, *Server) {
	t.Helper()
	if s == nil {
		s = &Server{AutoCreate: true}
	}
	if s.Root == "" {
		s.Root = t.TempDir()
	}
	ts := httptest.NewServer(s.Handler())
	t.Cleanup(ts.Close)
	return ts, s
}

func put(t *testing.T, r *repo.Repo, rel, content string) {
	t.Helper()
	p := filepath.Join(r.Root, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func commit(t *testing.T, r *repo.Repo, msg string) string {
	t.Helper()
	if _, err := r.Add([]string{r.Root}); err != nil {
		t.Fatal(err)
	}
	res, err := r.Commit(msg, author)
	if err != nil {
		t.Fatal(err)
	}
	return res.ID
}

func read(t *testing.T, r *repo.Repo, rel string) string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(r.Root, filepath.FromSlash(rel)))
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

// localWithRemote makes a repo with one commit and origin = url.
func localWithRemote(t *testing.T, url string) *repo.Repo {
	t.Helper()
	r, err := repo.Init(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if err := r.AddRemote("origin", url); err != nil {
		t.Fatal(err)
	}
	return r
}

func TestPushCloneFetchPullOverHTTP(t *testing.T) {
	var events []PushEvent
	var mu sync.Mutex
	ts, s := newServer(t, &Server{AutoCreate: true, OnPush: func(e PushEvent) {
		mu.Lock()
		events = append(events, e)
		mu.Unlock()
	}})
	url := ts.URL + "/ana/servo-board"

	// Ana creates the repo by pushing.
	ana := localWithRemote(t, url)
	put(t, ana, "netlist.json", netlist)
	put(t, ana, "firmware/main.cpp", "v1\n")
	first := commit(t, ana, "first")
	up, err := ana.Push("origin", "")
	if err != nil || up.Old != "" || up.New != first {
		t.Fatalf("first push = %+v, %v", up, err)
	}
	if got := s.Repos(); len(got) != 1 || got[0] != "ana/servo-board" {
		t.Fatalf("server repos = %v", got)
	}

	// Ben clones it.
	ben, err := repo.Clone(url, filepath.Join(t.TempDir(), repo.CloneName(url)))
	if err != nil {
		t.Fatal(err)
	}
	if filepath.Base(ben.Root) != "servo-board" || read(t, ben, "firmware/main.cpp") != "v1\n" {
		t.Fatal("clone didn't get the files")
	}

	// Ana pushes again; Ben's push is rejected until he pulls.
	put(t, ana, "netlist.json", strings.Replace(netlist, "GPIO9", "GPIO10", 1))
	anaTip := commit(t, ana, "servo to GPIO10")
	if _, err := ana.Push("origin", ""); err != nil {
		t.Fatal(err)
	}
	put(t, ben, "firmware/main.cpp", "v1\n// ben\n")
	commit(t, ben, "ben comment")
	if _, err := ben.Push("origin", ""); !errors.Is(err, repo.ErrNonFastForward) {
		t.Fatalf("ben push over ana: %v", err)
	}
	_, res, err := ben.Pull("origin", "", author)
	if err != nil || res.Commit == nil {
		t.Fatalf("ben pull = %+v, %v", res, err)
	}
	if !strings.Contains(read(t, ben, "netlist.json"), "GPIO10") {
		t.Fatal("pull didn't bring ana's wiring change")
	}
	benTip := res.Commit.ID
	if _, err := ben.Push("origin", ""); err != nil {
		t.Fatal(err)
	}

	// Ana pulls: fast-forward to Ben's merge.
	_, res, err = ana.Pull("origin", "", author)
	if err != nil || !res.FastForward || res.To != benTip {
		t.Fatalf("ana pull = %+v, %v", res, err)
	}

	// A new branch, then fetch sees it; deleting it on the server prunes it.
	if _, err := ana.CreateBranch("probe", ""); err != nil {
		t.Fatal(err)
	}
	if _, err := ana.Push("origin", "probe"); err != nil {
		t.Fatal(err)
	}
	ups, err := ben.Fetch("origin")
	if err != nil || len(ups) != 1 || ups[0].Branch != "probe" {
		t.Fatalf("fetch = %+v, %v", ups, err)
	}

	mu.Lock()
	defer mu.Unlock()
	if len(events) != 4 || events[0].Before != "" || events[1].Before != first || events[1].After != anaTip || events[3].Branch != "probe" {
		t.Fatalf("events = %+v", events)
	}
	if events[0].Objects == 0 || events[3].Objects != 0 {
		t.Fatalf("object counts = %d, %d (new branch on known commit sends nothing)", events[0].Objects, events[3].Objects)
	}
}

func TestFetchSendsOnlyNewObjects(t *testing.T) {
	ts, _ := newServer(t, nil)
	url := ts.URL + "/ana/board"
	ana := localWithRemote(t, url)
	put(t, ana, "big.txt", strings.Repeat("x", 1000))
	commit(t, ana, "one")
	ana.Push("origin", "")
	ben, err := repo.Clone(url, filepath.Join(t.TempDir(), "b"))
	if err != nil {
		t.Fatal(err)
	}
	put(t, ana, "small.txt", "s\n")
	tip := commit(t, ana, "two")
	ana.Push("origin", "")

	// Ask the server directly what it would send Ben.
	have, _ := ben.Refs().Branch("main")
	body, _ := json.Marshal(protocol.FetchRequest{Want: []string{tip}, Have: []string{have}})
	resp, err := http.Post(url+protocol.FetchPath, "application/json", bytes.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	scratch, _ := repo.Init(t.TempDir())
	ids, err := protocol.ReadBundle(resp.Body, scratch.Objects, 0)
	if err != nil {
		t.Fatal(err)
	}
	// New: commit, root tree, small.txt. big.txt is already at Ben's.
	if len(ids) != 3 || ids[len(ids)-1] != tip {
		t.Fatalf("sent %d objects %v", len(ids), ids)
	}
}

func TestPushRejectsBadBundles(t *testing.T) {
	ts, s := newServer(t, nil)
	url := ts.URL + "/ana/board"
	ana := localWithRemote(t, url)
	put(t, ana, "a.txt", "a\n")
	tip := commit(t, ana, "one")
	c, _ := ana.ReadCommit(tip)

	send := func(ids []string, newID string) *http.Response {
		var buf bytes.Buffer
		if err := protocol.WriteBundle(&buf, ana.Objects, ids); err != nil {
			t.Fatal(err)
		}
		q := "?branch=main&old=&new=" + newID
		resp, err := http.Post(url+protocol.PushPath+q, protocol.BundleType, &buf)
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		return resp
	}
	// Commit without its tree and file: refused, branch not created.
	if resp := send([]string{tip}, tip); resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("incomplete push: %d", resp.StatusCode)
	}
	server, _ := repo.Find(filepath.Join(s.Root, "ana", "board"))
	if id, _ := server.Refs().Branch("main"); id != "" {
		t.Fatal("incomplete push moved the branch")
	}
	// Pointing the branch at a tree (not a commit): refused.
	ids, _ := repo.ObjectsToSend(ana.Objects, []string{tip}, nil)
	if resp := send(ids, c.Tree); resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("tree as commit: %d", resp.StatusCode)
	}
	// Garbage body.
	resp, _ := http.Post(url+protocol.PushPath+"?branch=main&new="+tip, protocol.BundleType, strings.NewReader("nope"))
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("garbage: %d", resp.StatusCode)
	}
	// Too big.
	s.MaxPushBytes = 10
	if resp := send(ids, tip); resp.StatusCode != http.StatusRequestEntityTooLarge {
		t.Fatalf("too big: %d", resp.StatusCode)
	}
}

func TestBadNamesAndMissingRepos(t *testing.T) {
	ts, _ := newServer(t, &Server{})
	for _, p := range []string{"/../etc/info/refs", "/ana/..%2f..%2fx/info/refs", "/.hidden/x/info/refs", "/ana/nope/info/refs"} {
		resp, err := http.Get(ts.URL + p)
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		if resp.StatusCode != http.StatusNotFound {
			t.Errorf("GET %s = %d, want 404", p, resp.StatusCode)
		}
	}
	// Without AutoCreate a push to a missing repo is refused.
	ana := localWithRemote(t, ts.URL+"/ana/nope")
	put(t, ana, "a.txt", "a\n")
	commit(t, ana, "one")
	if _, err := ana.Push("origin", ""); err == nil || !strings.Contains(err.Error(), "not found") {
		t.Fatalf("push to missing repo: %v", err)
	}
	if _, err := repo.Clone(ts.URL+"/ana/nope", filepath.Join(t.TempDir(), "x")); err == nil {
		t.Fatal("clone of missing repo should fail")
	}
}

func TestAuthorize(t *testing.T) {
	ts, _ := newServer(t, &Server{AutoCreate: true, Authorize: func(r *http.Request, owner, name string, a Access) (string, error) {
		user, token, ok := r.BasicAuth()
		if !ok {
			return "", &HTTPError{http.StatusUnauthorized, "log in with a token"}
		}
		if token != "secret" {
			return "", &HTTPError{http.StatusForbidden, "bad token"}
		}
		if a == Write && user != owner {
			return "", &HTTPError{http.StatusForbidden, "you can't push here"}
		}
		return user, nil
	}})
	url := ts.URL + "/ana/board"
	ana := localWithRemote(t, url)
	put(t, ana, "a.txt", "a\n")
	commit(t, ana, "one")

	t.Setenv("KIRCH_USER", "")
	t.Setenv("KIRCH_TOKEN", "")
	if _, err := ana.Push("origin", ""); err == nil || !strings.Contains(err.Error(), "KIRCH_TOKEN") {
		t.Fatalf("no token: %v", err)
	}
	t.Setenv("KIRCH_USER", "ana")
	t.Setenv("KIRCH_TOKEN", "wrong")
	if _, err := ana.Push("origin", ""); err == nil || !strings.Contains(err.Error(), "bad token") {
		t.Fatalf("wrong token: %v", err)
	}
	t.Setenv("KIRCH_TOKEN", "secret")
	if _, err := ana.Push("origin", ""); err != nil {
		t.Fatal(err)
	}
	t.Setenv("KIRCH_USER", "ben")
	if _, err := repo.Clone(url, filepath.Join(t.TempDir(), "b")); err != nil {
		t.Fatalf("ben can read: %v", err)
	}
	put(t, ana, "b.txt", "b\n")
	commit(t, ana, "two")
	if _, err := ana.Push("origin", ""); err == nil || !strings.Contains(err.Error(), "can't push here") {
		t.Fatalf("ben pushing to ana's repo: %v", err)
	}
}

func TestTokenNotSavedInRemote(t *testing.T) {
	r, _ := repo.Init(t.TempDir())
	if err := r.AddRemote("origin", "https://ana:secret@example.com/ana/board"); err == nil {
		t.Fatal("URL with a token was accepted")
	}
	if err := r.AddRemote("origin", "https://example.com/ana/board/"); err != nil {
		t.Fatal(err)
	}
	if loc, _ := r.RemoteLocation("origin"); loc != "https://example.com/ana/board" {
		t.Fatalf("saved %q", loc)
	}
}

// Two pushes racing for the same branch: exactly one wins.
func TestConcurrentPushes(t *testing.T) {
	ts, _ := newServer(t, nil)
	u := ts.URL + "/ana/board"
	base := localWithRemote(t, u)
	put(t, base, "a.txt", "a\n")
	commit(t, base, "base")
	if _, err := base.Push("origin", ""); err != nil {
		t.Fatal(err)
	}
	var clones []*repo.Repo
	for i := 0; i < 4; i++ {
		c, err := repo.Clone(u, filepath.Join(t.TempDir(), "c"))
		if err != nil {
			t.Fatal(err)
		}
		put(t, c, "who.txt", string(rune('a'+i)))
		commit(t, c, "mine")
		clones = append(clones, c)
	}
	var wg sync.WaitGroup
	errs := make([]error, len(clones))
	for i, c := range clones {
		wg.Add(1)
		go func(i int, c *repo.Repo) {
			defer wg.Done()
			_, errs[i] = c.Push("origin", "")
		}(i, c)
	}
	wg.Wait()
	ok := 0
	for _, err := range errs {
		if err == nil {
			ok++
		} else if !errors.Is(err, repo.ErrNonFastForward) {
			t.Errorf("unexpected error: %v", err)
		}
	}
	if ok != 1 {
		t.Fatalf("%d pushes won, want exactly 1", ok)
	}
}

// Oracle: the server's repository is a valid git repository after pushes.
func TestServerRepoPassesGitFsck(t *testing.T) {
	git, err := exec.LookPath("git")
	if err != nil {
		t.Skip("git not installed")
	}
	ts, s := newServer(t, nil)
	ana := localWithRemote(t, ts.URL+"/ana/board")
	put(t, ana, "netlist.json", netlist)
	put(t, ana, "firmware/x.cpp", "x\n")
	commit(t, ana, "one")
	put(t, ana, "firmware/x.cpp", "y\n")
	commit(t, ana, "two")
	if _, err := ana.Push("origin", ""); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(git, "--git-dir="+filepath.Join(s.Root, "ana", "board", ".kirch"), "fsck", "--strict", "--no-dangling")
	cmd.Env = append(os.Environ(), "GIT_CONFIG_NOSYSTEM=1")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git fsck: %v\n%s", err, out)
	}
}
