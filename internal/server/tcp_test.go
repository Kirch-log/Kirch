package server

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Kirch-log/kirch/internal/objects"
	"github.com/Kirch-log/kirch/internal/proto"
	"github.com/Kirch-log/kirch/internal/repo"
)

var author = objects.Signature{Name: "Test", Email: "test@example.com", When: time.Unix(1700000000, 0).UTC()}

// testServer runs a TCPServer on 127.0.0.1 with a fresh self-signed
// certificate that clients in this test trust (via KIRCH_CA_FILE).
type testServer struct {
	*TCPServer
	addr   string
	mu     sync.Mutex
	pushes []string
}

func startTCP(t *testing.T) *testServer {
	t.Helper()
	certPEM, keyPEM, err := SelfSigned(nil)
	if err != nil {
		t.Fatal(err)
	}
	caFile := filepath.Join(t.TempDir(), "ca.pem")
	if err := os.WriteFile(caFile, certPEM, 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("KIRCH_CA_FILE", caFile)
	cert, err := tls.X509KeyPair(certPEM, keyPEM)
	if err != nil {
		t.Fatal(err)
	}
	ln, err := tls.Listen("tcp", "127.0.0.1:0", &tls.Config{Certificates: []tls.Certificate{cert}})
	if err != nil {
		t.Fatal(err)
	}
	ts := &testServer{addr: ln.Addr().String()}
	ts.TCPServer = &TCPServer{
		Repos: Repos{DataDir: t.TempDir()},
		OnPush: func(_ context.Context, owner, name string) error {
			ts.mu.Lock()
			ts.pushes = append(ts.pushes, owner+"/"+name)
			ts.mu.Unlock()
			return nil
		},
	}
	done := make(chan struct{})
	go func() { ts.Serve(ln); close(done) }()
	t.Cleanup(func() { ln.Close(); <-done })
	return ts
}

func (ts *testServer) url(repoName string) string {
	return "kirch://" + ts.addr + "/" + repoName
}

func newRepo(t *testing.T) *repo.Repo {
	t.Helper()
	r, err := repo.Init(filepath.Join(t.TempDir(), "work"))
	if err != nil {
		t.Fatal(err)
	}
	return r
}

func write(t *testing.T, r *repo.Repo, rel, content string) {
	t.Helper()
	p := filepath.Join(r.Root, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

var clock int64

func commit(t *testing.T, r *repo.Repo, msg string) string {
	t.Helper()
	if _, err := r.Add([]string{r.Root}); err != nil {
		t.Fatal(err)
	}
	clock++
	a := author
	a.When = author.When.Add(time.Duration(clock) * time.Minute)
	res, err := r.Commit(msg, a)
	if err != nil {
		t.Fatal(err)
	}
	return res.ID
}

func netlist(pin string) string {
	return fmt.Sprintf(`{"format":"kirch.netlist/1","nets":{"GND":["SERVO1.GND","U1.GND"],"SIG":["SERVO1.SIG","%s"]}}`, pin)
}

func serverTip(t *testing.T, ts *testServer, owner, name, branch string) string {
	t.Helper()
	r, err := ts.Repos.Open(owner, name)
	if err != nil {
		t.Fatal(err)
	}
	id, err := r.Refs().Branch(branch)
	if err != nil {
		t.Fatal(err)
	}
	return id
}

func TestPushCloneFetchPull(t *testing.T) {
	ts := startTCP(t)
	a := newRepo(t)
	write(t, a, "netlist.json", netlist("U1.GPIO9"))
	write(t, a, "firmware/main.cpp", "int main() {}\n")
	c1 := commit(t, a, "servo on GPIO9")
	if err := a.AddRemote("origin", ts.url("evin/servo-board")); err != nil {
		t.Fatal(err)
	}
	up, err := a.Push("origin", "")
	if err != nil {
		t.Fatal(err)
	}
	if up.Old != "" || up.New != c1 {
		t.Fatalf("push: got %+v", up)
	}
	if got := serverTip(t, ts, "evin", "servo-board", "main"); got != c1 {
		t.Fatalf("server main = %s, want %s", got, c1)
	}

	// Clone over kirch:// gets the same files.
	dest := filepath.Join(t.TempDir(), "clone")
	b, err := repo.Clone(ts.url("evin/servo-board"), dest)
	if err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(filepath.Join(dest, "firmware", "main.cpp"))
	if err != nil || string(got) != "int main() {}\n" {
		t.Fatalf("cloned file: %q, %v", got, err)
	}
	if tip, _ := b.Refs().Branch("main"); tip != c1 {
		t.Fatalf("clone main = %s", tip)
	}

	// b pushes a change, a pulls it (fast-forward).
	write(t, b, "netlist.json", netlist("U1.GPIO10"))
	c2 := commit(t, b, "servo to GPIO10")
	if _, err := b.Push("origin", "main"); err != nil {
		t.Fatal(err)
	}
	_, res, err := a.Pull("origin", "", author)
	if err != nil {
		t.Fatal(err)
	}
	if !res.FastForward || res.To != c2 {
		t.Fatalf("pull: %+v", res)
	}
	if tip, _ := a.Refs().RemoteBranch("origin", "main"); tip != c2 {
		t.Fatalf("origin/main = %s", tip)
	}

	// Pushing again changes nothing.
	up, err = a.Push("origin", "")
	if err != nil || up.Old != up.New {
		t.Fatalf("second push: %+v %v", up, err)
	}

	ts.mu.Lock()
	defer ts.mu.Unlock()
	if strings.Join(ts.pushes, ",") != "evin/servo-board,evin/servo-board" {
		t.Errorf("OnPush calls: %v", ts.pushes)
	}
}

func TestPushRefusesNonFastForward(t *testing.T) {
	ts := startTCP(t)
	a := newRepo(t)
	write(t, a, "netlist.json", netlist("U1.GPIO9"))
	commit(t, a, "base")
	a.AddRemote("origin", ts.url("o/r"))
	if _, err := a.Push("origin", ""); err != nil {
		t.Fatal(err)
	}
	b, err := repo.Clone(ts.url("o/r"), filepath.Join(t.TempDir(), "b"))
	if err != nil {
		t.Fatal(err)
	}
	write(t, a, "netlist.json", netlist("U1.GPIO10"))
	ca := commit(t, a, "a's change")
	if _, err := a.Push("origin", ""); err != nil {
		t.Fatal(err)
	}
	write(t, b, "netlist.json", netlist("U1.GPIO11"))
	commit(t, b, "b's change")
	if _, err := b.Push("origin", ""); !errors.Is(err, repo.ErrNonFastForward) {
		t.Fatalf("got %v, want ErrNonFastForward", err)
	}
	if got := serverTip(t, ts, "o", "r", "main"); got != ca {
		t.Fatalf("server moved to %s", got)
	}
}

func TestPushNewBranchSetsHeadForEmptyRepo(t *testing.T) {
	ts := startTCP(t)
	a := newRepo(t)
	write(t, a, "x.txt", "x")
	commit(t, a, "x")
	if _, err := a.CreateBranch("dev", ""); err != nil {
		t.Fatal(err)
	}
	a.AddRemote("origin", ts.url("o/dev-only"))
	if _, err := a.Push("origin", "dev"); err != nil {
		t.Fatal(err)
	}
	b, err := repo.Clone(ts.url("o/dev-only"), filepath.Join(t.TempDir(), "b"))
	if err != nil {
		t.Fatal(err)
	}
	if head, id, _ := b.Refs().Head(); head != "dev" || id == "" {
		t.Fatalf("clone HEAD = %s %s, want dev checked out", head, id)
	}
}

func TestCloneUnknownRepoLeavesNothing(t *testing.T) {
	ts := startTCP(t)
	dest := filepath.Join(t.TempDir(), "nope")
	_, err := repo.Clone(ts.url("o/missing"), dest)
	if err == nil || !strings.Contains(err.Error(), "no repository o/missing") {
		t.Fatalf("got %v", err)
	}
	if _, err := os.Stat(dest); !os.IsNotExist(err) {
		t.Fatalf("failed clone left %s behind", dest)
	}
}

// raw opens a protocol connection without the repo package, to send things
// a well-behaved client never would.
func raw(t *testing.T, ts *testServer) *proto.Conn {
	t.Helper()
	cfg, err := proto.TLSConfig("127.0.0.1")
	if err != nil {
		t.Fatal(err)
	}
	nc, err := tls.Dial("tcp", ts.addr, cfg)
	if err != nil {
		t.Fatal(err)
	}
	c := proto.NewConn(nc)
	t.Cleanup(func() { c.Close() })
	return c
}

func expectErr(t *testing.T, c *proto.Conn, code string) {
	t.Helper()
	var ok proto.OKMsg
	err := c.Expect(proto.OK, &ok)
	var pe *proto.Error
	if !errors.As(err, &pe) || pe.Code != code {
		t.Fatalf("got %v, want ERR %s", err, code)
	}
}

func hello(t *testing.T, c *proto.Conn, h proto.HelloMsg) {
	t.Helper()
	if err := c.Send(proto.Hello, h); err != nil {
		t.Fatal(err)
	}
	c.Flush()
}

func TestBadHello(t *testing.T) {
	ts := startTCP(t)
	cases := map[string]proto.HelloMsg{
		proto.CodeVersion:    {Version: 99, Repo: "o/r", Op: proto.OpFetch},
		proto.CodeBadRequest: {Version: 1, Repo: "../etc", Op: proto.OpPush},
	}
	for code, h := range cases {
		c := raw(t, ts)
		hello(t, c, h)
		expectErr(t, c, code)
	}
	c := raw(t, ts)
	hello(t, c, proto.HelloMsg{Version: 1, Repo: "o/r", Op: "delete"})
	expectErr(t, c, proto.CodeBadRequest)
	if _, err := os.Stat(filepath.Join(ts.Repos.DataDir, "repos", "o")); !os.IsNotExist(err) {
		t.Error("bad requests must not create repositories")
	}
}

// pushOne sets up o/r on the server with one commit and returns its id.
func pushOne(t *testing.T, ts *testServer) (*repo.Repo, string) {
	a := newRepo(t)
	write(t, a, "netlist.json", netlist("U1.GPIO9"))
	id := commit(t, a, "base")
	a.AddRemote("origin", ts.url("o/r"))
	if _, err := a.Push("origin", ""); err != nil {
		t.Fatal(err)
	}
	return a, id
}

func TestPushMissingObjectsRefused(t *testing.T) {
	ts := startTCP(t)
	a, base := pushOne(t, ts)
	write(t, a, "netlist.json", netlist("U1.GPIO10"))
	next := commit(t, a, "next")

	c := raw(t, ts)
	hello(t, c, proto.HelloMsg{Version: 1, Repo: "o/r", Op: proto.OpPush})
	var refsMsg proto.RefsMsg
	if err := c.Expect(proto.Refs, &refsMsg); err != nil {
		t.Fatal(err)
	}
	// Send only the commit object, not its tree or blob.
	_, data, _ := a.Objects.Read(next)
	c.SendObject(objects.Commit, data)
	c.Send(proto.Done, proto.DoneMsg{Count: 1})
	c.Send(proto.Update, proto.UpdateMsg{Branch: "main", Old: base, New: next})
	c.Flush()
	expectErr(t, c, proto.CodeMissingObjects)
	if got := serverTip(t, ts, "o", "r", "main"); got != base {
		t.Fatalf("branch moved to %s", got)
	}
}

func TestPushStaleOldRefused(t *testing.T) {
	ts := startTCP(t)
	a, base := pushOne(t, ts)
	write(t, a, "netlist.json", netlist("U1.GPIO10"))
	next := commit(t, a, "next")
	ids, err := a.Objects.Missing([]string{next}, nil)
	if err != nil {
		t.Fatal(err)
	}
	c := raw(t, ts)
	hello(t, c, proto.HelloMsg{Version: 1, Repo: "o/r", Op: proto.OpPush})
	var refsMsg proto.RefsMsg
	c.Expect(proto.Refs, &refsMsg)
	c.SendObjects(a.Objects, ids)
	c.Send(proto.Update, proto.UpdateMsg{Branch: "main", Old: "", New: next}) // claims branch is new
	c.Flush()
	expectErr(t, c, proto.CodeStale)
	if got := serverTip(t, ts, "o", "r", "main"); got != base {
		t.Fatalf("branch moved to %s", got)
	}
}

func TestPushBadBranchName(t *testing.T) {
	ts := startTCP(t)
	_, base := pushOne(t, ts)
	c := raw(t, ts)
	hello(t, c, proto.HelloMsg{Version: 1, Repo: "o/r", Op: proto.OpPush})
	var refsMsg proto.RefsMsg
	c.Expect(proto.Refs, &refsMsg)
	c.Send(proto.Done, proto.DoneMsg{})
	c.Send(proto.Update, proto.UpdateMsg{Branch: "../../HEAD", Old: "", New: base})
	c.Flush()
	expectErr(t, c, proto.CodeBadRequest)
}

func TestFetchUnknownWant(t *testing.T) {
	ts := startTCP(t)
	pushOne(t, ts)
	c := raw(t, ts)
	hello(t, c, proto.HelloMsg{Version: 1, Repo: "o/r", Op: proto.OpFetch})
	var refsMsg proto.RefsMsg
	c.Expect(proto.Refs, &refsMsg)
	c.Send(proto.Want, proto.WantMsg{Wants: []string{strings.Repeat("a", 40)}})
	c.Flush()
	expectErr(t, c, proto.CodeBadRequest)
}

func TestConcurrentPushesOneWins(t *testing.T) {
	ts := startTCP(t)
	_, base := pushOne(t, ts)
	const n = 6
	var clones []*repo.Repo
	for i := 0; i < n; i++ {
		b, err := repo.Clone(ts.url("o/r"), filepath.Join(t.TempDir(), fmt.Sprint("c", i)))
		if err != nil {
			t.Fatal(err)
		}
		write(t, b, "netlist.json", netlist(fmt.Sprintf("U1.GPIO%d", 20+i)))
		commit(t, b, fmt.Sprint("change ", i))
		clones = append(clones, b)
	}
	var wg sync.WaitGroup
	errs := make([]error, n)
	for i, b := range clones {
		wg.Add(1)
		go func() { defer wg.Done(); _, errs[i] = b.Push("origin", "") }()
	}
	wg.Wait()
	wins := 0
	for _, err := range errs {
		if err == nil {
			wins++
		} else if !errors.Is(err, repo.ErrNonFastForward) && !strings.Contains(err.Error(), "moved") {
			t.Errorf("unexpected error: %v", err)
		}
	}
	if wins != 1 {
		t.Fatalf("%d pushes won, want exactly 1", wins)
	}
	r, _ := ts.Repos.Open("o", "r")
	tip, _ := r.Refs().Branch("main")
	if tip == base {
		t.Fatal("no push landed")
	}
	if err := r.Objects.CheckComplete(tip, nil); err != nil {
		t.Fatal(err)
	}
}

func TestUntrustedCertRefused(t *testing.T) {
	ts := startTCP(t)
	t.Setenv("KIRCH_CA_FILE", "")
	a := newRepo(t)
	write(t, a, "x", "x")
	commit(t, a, "x")
	a.AddRemote("origin", ts.url("o/r"))
	if _, err := a.Push("origin", ""); err == nil || !strings.Contains(err.Error(), "certificate") {
		t.Fatalf("got %v, want certificate error", err)
	}
}

func TestFetchTooManyHaves(t *testing.T) {
	ts := startTCP(t)
	pushOne(t, ts)
	c := raw(t, ts)
	hello(t, c, proto.HelloMsg{Version: 1, Repo: "o/r", Op: proto.OpFetch})
	var refsMsg proto.RefsMsg
	c.Expect(proto.Refs, &refsMsg)
	haves := make([]string, maxTips+1)
	for i := range haves {
		haves[i] = strings.Repeat("a", 40)
	}
	c.Send(proto.Want, proto.WantMsg{Haves: haves})
	c.Flush()
	expectErr(t, c, proto.CodeBadRequest)
}
