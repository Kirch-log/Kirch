// Package server is the Kirch server: kirch:// push / pull over TCP + TLS,
// and the HTTP API the website reads.
package server

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"os"
	"path/filepath"
	"sync"

	"github.com/Kirch-log/kirch/internal/objects"
	"github.com/Kirch-log/kirch/internal/proto"
	"github.com/Kirch-log/kirch/internal/refs"
	"github.com/Kirch-log/kirch/internal/repo"
)

// Repos is where the server keeps repositories:
// <DataDir>/repos/<owner>/<name>/.kirch. Repo folders are the source of
// truth; Postgres is an index built from them.
type Repos struct {
	DataDir string
}

// Path is the working folder of owner/name (it holds .kirch; no files are
// ever checked out on the server).
func (rs Repos) Path(owner, name string) string {
	return filepath.Join(rs.DataDir, "repos", owner, name)
}

// Open opens owner/name, or returns an error wrapping fs.ErrNotExist.
func (rs Repos) Open(owner, name string) (*repo.Repo, error) {
	if err := proto.CheckRepoName(owner); err != nil {
		return nil, err
	}
	if err := proto.CheckRepoName(name); err != nil {
		return nil, err
	}
	p := rs.Path(owner, name)
	if _, err := os.Stat(filepath.Join(p, repo.DirName, "HEAD")); err != nil {
		return nil, fmt.Errorf("repository %s/%s: %w", owner, name, os.ErrNotExist)
	}
	return repo.Open(p)
}

// openOrCreate opens owner/name, creating it if it doesn't exist yet.
func (rs Repos) openOrCreate(owner, name string) (*repo.Repo, error) {
	r, err := rs.Open(owner, name)
	if err == nil || !errors.Is(err, os.ErrNotExist) {
		return r, err
	}
	r, err = repo.Init(rs.Path(owner, name))
	if errors.Is(err, repo.ErrExists) {
		return rs.Open(owner, name)
	}
	return r, err
}

// List returns every owner/name stored on disk.
func (rs Repos) List() ([][2]string, error) {
	owners, err := os.ReadDir(filepath.Join(rs.DataDir, "repos"))
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var out [][2]string
	for _, o := range owners {
		if !o.IsDir() || proto.CheckRepoName(o.Name()) != nil {
			continue
		}
		names, err := os.ReadDir(filepath.Join(rs.DataDir, "repos", o.Name()))
		if err != nil {
			return nil, err
		}
		for _, n := range names {
			if n.IsDir() && proto.CheckRepoName(n.Name()) == nil {
				if _, err := rs.Open(o.Name(), n.Name()); err == nil {
					out = append(out, [2]string{o.Name(), n.Name()})
				}
			}
		}
	}
	return out, nil
}

// TCPServer serves kirch:// connections.
type TCPServer struct {
	Repos Repos
	// OnPush runs after a push moved a branch (the indexer). Errors are
	// logged; the push has already succeeded.
	OnPush func(ctx context.Context, owner, name string) error
	Log    *log.Logger
	// MaxConns limits connections handled at once (default 64).
	MaxConns int

	mu    sync.Mutex
	locks map[string]*sync.Mutex
	wg    sync.WaitGroup
}

func (s *TCPServer) logf(format string, args ...any) {
	if s.Log != nil {
		s.Log.Printf(format, args...)
	}
}

// repoLock serializes pushes to one repository.
func (s *TCPServer) repoLock(key string) *sync.Mutex {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.locks == nil {
		s.locks = map[string]*sync.Mutex{}
	}
	if s.locks[key] == nil {
		s.locks[key] = &sync.Mutex{}
	}
	return s.locks[key]
}

// Serve accepts connections on ln (already wrapped in TLS) until ln closes,
// then waits for open connections to finish.
func (s *TCPServer) Serve(ln net.Listener) error {
	max := s.MaxConns
	if max <= 0 {
		max = 64
	}
	sem := make(chan struct{}, max)
	defer s.wg.Wait()
	for {
		nc, err := ln.Accept()
		if err != nil {
			if errors.Is(err, net.ErrClosed) {
				return nil
			}
			return err
		}
		sem <- struct{}{}
		s.wg.Add(1)
		go func() {
			defer func() { <-sem; s.wg.Done() }()
			s.handle(nc)
		}()
	}
}

func (s *TCPServer) handle(nc net.Conn) {
	c := proto.NewConn(nc)
	defer c.Close()
	err := s.serve(c)
	if err == nil || errors.Is(err, io.EOF) {
		return
	}
	var pe *proto.Error
	if !errors.As(err, &pe) {
		s.logf("tcp %s: %v", nc.RemoteAddr(), err)
		pe = proto.Errorf(proto.CodeInternal, "server error")
	}
	c.SendError(pe)
}

func (s *TCPServer) serve(c *proto.Conn) error {
	var h proto.HelloMsg
	if err := c.Expect(proto.Hello, &h); err != nil {
		return err
	}
	if h.Version != proto.Version {
		return proto.Errorf(proto.CodeVersion, "server speaks protocol version %d, client sent %d (update kirch)", proto.Version, h.Version)
	}
	owner, name, err := proto.SplitRepo(h.Repo)
	if err != nil {
		return proto.Errorf(proto.CodeBadRequest, "%v", err)
	}
	switch h.Op {
	case proto.OpFetch:
		return s.fetch(c, owner, name)
	case proto.OpPush:
		lock := s.repoLock(owner + "/" + name)
		lock.Lock()
		defer lock.Unlock()
		return s.push(c, owner, name)
	}
	return proto.Errorf(proto.CodeBadRequest, "unknown op %q", h.Op)
}

func sendRefs(c *proto.Conn, r *repo.Repo) (map[string]string, error) {
	branches, err := r.Refs().Branches()
	if err != nil {
		return nil, err
	}
	head, _, err := r.Refs().Head()
	if err != nil {
		return nil, err
	}
	if err := c.Send(proto.Refs, proto.RefsMsg{Head: head, Branches: branches}); err != nil {
		return nil, err
	}
	return branches, c.Flush()
}

func (s *TCPServer) fetch(c *proto.Conn, owner, name string) error {
	r, err := s.Repos.Open(owner, name)
	if errors.Is(err, os.ErrNotExist) {
		return proto.Errorf(proto.CodeNotFound, "no repository %s/%s on this server", owner, name)
	}
	if err != nil {
		return err
	}
	if _, err := sendRefs(c, r); err != nil {
		return err
	}
	var w proto.WantMsg
	if err := c.Expect(proto.Want, &w); err != nil {
		return err
	}
	for _, id := range w.Wants {
		if !objects.ValidID(id) || !r.Objects.Has(id) {
			return proto.Errorf(proto.CodeBadRequest, "unknown object %q", id)
		}
	}
	have, err := r.Objects.HaveSet(w.Haves)
	if err != nil {
		return err
	}
	ids, err := r.Objects.Missing(w.Wants, have)
	if err != nil {
		return err
	}
	return c.SendObjects(r.Objects, ids)
}

func (s *TCPServer) push(c *proto.Conn, owner, name string) error {
	r, err := s.Repos.openOrCreate(owner, name)
	if err != nil {
		return err
	}
	before, err := sendRefs(c, r)
	if err != nil {
		return err
	}
	// The client may hang up here when there's nothing to push.
	n, err := c.ReceiveObjects(r.Objects)
	if errors.Is(err, io.EOF) && n == 0 {
		return nil
	}
	if err != nil {
		return err
	}
	var u proto.UpdateMsg
	if err := c.Expect(proto.Update, &u); err != nil {
		return err
	}
	if err := refs.CheckBranchName(u.Branch); err != nil {
		return proto.Errorf(proto.CodeBadRequest, "%v", err)
	}
	if !objects.ValidID(u.New) || (u.Old != "" && !objects.ValidID(u.Old)) {
		return proto.Errorf(proto.CodeBadRequest, "bad commit id in UPDATE")
	}
	known := map[string]bool{}
	for _, id := range before {
		known[id] = true
	}
	if err := r.Objects.CheckComplete(u.New, known); err != nil {
		return proto.Errorf(proto.CodeMissingObjects, "push is missing objects, nothing changed: %v", err)
	}
	current, err := r.Refs().Branch(u.Branch)
	if err != nil {
		return err
	}
	if current != u.Old {
		return proto.Errorf(proto.CodeStale, "branch %s moved on the server; pull, then push again", u.Branch)
	}
	if u.Old != "" && u.Old != u.New {
		ok, err := r.IsAncestor(u.Old, u.New)
		if err != nil {
			return err
		}
		if !ok {
			return proto.Errorf(proto.CodeNonFastForward, "the server's %s has commits you don't have; pull first, then push", u.Branch)
		}
	}
	if err := r.Refs().UpdateBranch(u.Branch, u.New, u.Old); err != nil {
		if errors.Is(err, refs.ErrStale) {
			return proto.Errorf(proto.CodeStale, "branch %s moved on the server; pull, then push again", u.Branch)
		}
		return err
	}
	// A new repository's HEAD names "main"; if that was never pushed, point
	// HEAD at the first branch that was, so clones check something out.
	if head, id, err := r.Refs().Head(); err == nil && id == "" && head != u.Branch {
		if err := r.Refs().SetHead(u.Branch); err != nil {
			return err
		}
	}
	s.logf("push %s/%s %s %s -> %s (%d objects)", owner, name, u.Branch, short(u.Old), short(u.New), n)
	// Index before saying OK, so once a push returns the website shows it.
	// The branch has already moved: a failed index is logged, not refused,
	// and the next push or a restart catches up.
	if s.OnPush != nil {
		if err := s.OnPush(context.Background(), owner, name); err != nil {
			s.logf("index %s/%s after push: %v", owner, name, err)
		}
	}
	if err := c.Send(proto.OK, proto.OKMsg{}); err != nil {
		return err
	}
	return c.Flush()
}

func short(id string) string {
	if len(id) < 7 {
		return "(new)"
	}
	return id[:7]
}
