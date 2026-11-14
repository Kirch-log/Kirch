package repo

import (
	"context"
	"errors"
	"fmt"

	"github.com/Kirch-log/kirch/internal/objects"
	"github.com/Kirch-log/kirch/internal/proto"
	"github.com/Kirch-log/kirch/internal/refs"
)

// remoteEnd is the other side of a fetch or push: another folder on this
// computer, or a Kirch server reached over kirch://.
type remoteEnd interface {
	// branches and head are what the remote had when the connection opened.
	branches() map[string]string
	head() string
	// fetch stores in dst every object reachable from wants that isn't
	// reachable from haves.
	fetch(dst *objects.Store, wants, haves []string) error
	// push sends what the remote is missing for new and moves its branch
	// from old to new.
	push(src *objects.Store, branch, old, new string) error
	close() error
}

// openEnd connects to a remote for one operation (proto.OpFetch / OpPush).
func (r *Repo) openEnd(remote, op string) (remoteEnd, error) {
	loc, err := r.RemoteLocation(remote)
	if err != nil {
		return nil, err
	}
	if proto.IsURL(loc) {
		cl, err := proto.Dial(context.Background(), loc, op)
		if err != nil {
			return nil, fmt.Errorf("remote %q: %w", remote, err)
		}
		return &serverEnd{cl: cl}, nil
	}
	rem, _, err := r.OpenRemote(remote)
	if err != nil {
		return nil, err
	}
	return newFolderEnd(rem, remote)
}

// folderEnd is a repository folder on this computer.
type folderEnd struct {
	rem      *Repo
	name     string
	tips     map[string]string
	headName string
}

func newFolderEnd(rem *Repo, name string) (*folderEnd, error) {
	tips, err := rem.Refs().Branches()
	if err != nil {
		return nil, err
	}
	head, _, err := rem.Refs().Head()
	if err != nil {
		return nil, err
	}
	return &folderEnd{rem: rem, name: name, tips: tips, headName: head}, nil
}

func (f *folderEnd) branches() map[string]string { return f.tips }
func (f *folderEnd) head() string                { return f.headName }
func (f *folderEnd) close() error                { return nil }

func (f *folderEnd) fetch(dst *objects.Store, wants, _ []string) error {
	_, err := copyObjects(f.rem.Objects, dst, wants)
	return err
}

func (f *folderEnd) push(src *objects.Store, branch, old, new string) error {
	if _, err := copyObjects(src, f.rem.Objects, []string{new}); err != nil {
		return err
	}
	remoteCurrent, _, err := f.rem.Refs().Head()
	if err != nil {
		return err
	}
	if remoteCurrent == branch {
		if err := f.rem.updateCheckedOut(new); err != nil {
			return fmt.Errorf("remote %q has %s checked out: %w", f.name, branch, err)
		}
	}
	return f.rem.Refs().UpdateBranch(branch, new, old)
}

// serverEnd is a Kirch server reached over kirch://.
type serverEnd struct {
	cl *proto.Client
}

func (s *serverEnd) branches() map[string]string { return s.cl.Refs.Branches }
func (s *serverEnd) head() string                { return s.cl.Refs.Head }
func (s *serverEnd) close() error                { return s.cl.Close() }

func (s *serverEnd) fetch(dst *objects.Store, wants, haves []string) error {
	_, err := s.cl.Fetch(dst, wants, haves)
	return serverErr(err)
}

func (s *serverEnd) push(src *objects.Store, branch, old, new string) error {
	var tips []string
	for _, id := range s.cl.Refs.Branches {
		tips = append(tips, id)
	}
	have, err := src.HaveSet(tips)
	if err != nil {
		return err
	}
	ids, err := src.Missing([]string{new}, have)
	if err != nil {
		return err
	}
	return serverErr(s.cl.Push(src, ids, branch, old, new))
}

// serverErr turns protocol refusals into the errors folder remotes give.
func serverErr(err error) error {
	var pe *proto.Error
	if !errors.As(err, &pe) {
		return err
	}
	switch pe.Code {
	case proto.CodeNonFastForward:
		return ErrNonFastForward
	case proto.CodeStale:
		return fmt.Errorf("%w (someone pushed while you were pushing; kirch pull, then push again)", refs.ErrStale)
	}
	return fmt.Errorf("server refused: %s", pe.Message)
}
