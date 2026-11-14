// Package refs reads and moves branch pointers.
//
//	.kirch/HEAD               "ref: refs/heads/main\n"  (current branch)
//	.kirch/refs/heads/main    "<commit id>\n"            (tip of the branch)
//
// A branch with no commits yet has no file. Updates use a lock file and a
// compare-and-swap, like git: if someone else moved the branch since we read
// it, the update fails instead of silently losing their commit.
package refs

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/Kirch-log/kirch/internal/objects"
)

var (
	// ErrStale means the branch moved since it was read.
	ErrStale = errors.New("branch was changed by someone else; try again")
	// ErrLocked means another kirch command is updating the same branch.
	ErrLocked = errors.New("branch is locked by another kirch command (delete the .lock file if none is running)")
)

// Store reads and writes refs under a .kirch folder.
type Store struct {
	Dir string // the .kirch folder
}

// New returns a Store for the given .kirch folder.
func New(dir string) *Store { return &Store{Dir: dir} }

// Head returns the branch HEAD points to (e.g. "main") and its commit id
// ("" when the branch has no commits yet).
func (s *Store) Head() (branch, id string, err error) {
	data, err := os.ReadFile(filepath.Join(s.Dir, "HEAD"))
	if err != nil {
		return "", "", err
	}
	line := strings.TrimSpace(string(data))
	target, ok := strings.CutPrefix(line, "ref: refs/heads/")
	if !ok {
		return "", "", fmt.Errorf("HEAD is %q; detached HEAD isn't supported yet", line)
	}
	id, err = s.Branch(target)
	return target, id, err
}

// Branch returns the commit id a branch points to, or "" if it has none.
func (s *Store) Branch(name string) (string, error) {
	if err := CheckBranchName(name); err != nil {
		return "", err
	}
	data, err := os.ReadFile(s.branchPath(name))
	if errors.Is(err, os.ErrNotExist) {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	id := strings.TrimSpace(string(data))
	if !objects.ValidID(id) {
		return "", fmt.Errorf("branch %s is damaged: %q", name, id)
	}
	return id, nil
}

// UpdateBranch points branch name at newID, but only if it currently points
// at oldID ("" = branch must not exist yet).
func (s *Store) UpdateBranch(name, newID, oldID string) error {
	if err := CheckBranchName(name); err != nil {
		return err
	}
	if !objects.ValidID(newID) {
		return fmt.Errorf("bad commit id %q", newID)
	}
	path := s.branchPath(name)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	lock := path + ".lock"
	f, err := os.OpenFile(lock, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644)
	if errors.Is(err, os.ErrExist) {
		return ErrLocked
	}
	if err != nil {
		return err
	}
	written := false
	defer func() {
		if !written {
			os.Remove(lock)
		}
	}()

	current, err := s.Branch(name)
	if err != nil {
		f.Close()
		return err
	}
	if current != oldID {
		f.Close()
		return ErrStale
	}
	if _, err := f.WriteString(newID + "\n"); err != nil {
		f.Close()
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	if err := os.Rename(lock, path); err != nil {
		return err
	}
	written = true
	return nil
}

func (s *Store) branchPath(name string) string {
	return filepath.Join(s.Dir, "refs", "heads", filepath.FromSlash(name))
}

// CheckBranchName applies a simplified version of git's branch name rules.
func CheckBranchName(name string) error {
	bad := name == "" || name == "HEAD" ||
		strings.HasPrefix(name, "-") || strings.HasPrefix(name, "/") || strings.HasSuffix(name, "/") ||
		strings.HasSuffix(name, ".lock") || strings.HasSuffix(name, ".") ||
		strings.Contains(name, "..") || strings.Contains(name, "//") || strings.Contains(name, "@{") ||
		strings.ContainsAny(name, " ~^:?*[\\\x7f")
	for _, r := range name {
		if r < 0x20 {
			bad = true
		}
	}
	for _, part := range strings.Split(name, "/") {
		if strings.HasPrefix(part, ".") {
			bad = true
		}
	}
	if bad {
		return fmt.Errorf("%q is not a valid branch name", name)
	}
	return nil
}

// Branches returns every branch and the commit it points to.
func (s *Store) Branches() (map[string]string, error) {
	heads := filepath.Join(s.Dir, "refs", "heads")
	out := map[string]string{}
	err := filepath.WalkDir(heads, func(p string, d os.DirEntry, err error) error {
		if err != nil {
			if errors.Is(err, os.ErrNotExist) && p == heads {
				return nil
			}
			return err
		}
		if d.IsDir() || strings.HasSuffix(p, ".lock") {
			return nil
		}
		rel, err := filepath.Rel(heads, p)
		if err != nil {
			return err
		}
		name := filepath.ToSlash(rel)
		id, err := s.Branch(name)
		if err != nil {
			return err
		}
		out[name] = id
		return nil
	})
	return out, err
}

// SetHead makes HEAD point to a branch (the branch may have no commits yet).
func (s *Store) SetHead(branch string) error {
	if err := CheckBranchName(branch); err != nil {
		return err
	}
	path := filepath.Join(s.Dir, "HEAD")
	lock := path + ".lock"
	f, err := os.OpenFile(lock, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644)
	if errors.Is(err, os.ErrExist) {
		return ErrLocked
	}
	if err != nil {
		return err
	}
	_, err = f.WriteString("ref: refs/heads/" + branch + "\n")
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err == nil {
		err = os.Rename(lock, path)
	}
	if err != nil {
		os.Remove(lock)
	}
	return err
}

// DeleteBranch removes a branch, but only if it still points at oldID.
func (s *Store) DeleteBranch(name, oldID string) error {
	if err := CheckBranchName(name); err != nil {
		return err
	}
	path := s.branchPath(name)
	lock := path + ".lock"
	f, err := os.OpenFile(lock, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644)
	if errors.Is(err, os.ErrExist) {
		return ErrLocked
	}
	if err != nil {
		return err
	}
	f.Close()
	defer os.Remove(lock)
	current, err := s.Branch(name)
	if err != nil {
		return err
	}
	if current != oldID {
		return ErrStale
	}
	if err := os.Remove(path); err != nil {
		return err
	}
	os.Remove(lock)
	// Drop folders left empty by names like feature/x.
	heads := filepath.Join(s.Dir, "refs", "heads")
	for dir := filepath.Dir(path); dir != heads; dir = filepath.Dir(dir) {
		if os.Remove(dir) != nil {
			break
		}
	}
	return nil
}

// Remote-tracking branches: the last known tip of a branch in another
// repository, as git stores them.
//
//	.kirch/refs/remotes/origin/main   "<commit id>\n"

func (s *Store) remotePath(remote, branch string) string {
	return filepath.Join(s.Dir, "refs", "remotes", remote, filepath.FromSlash(branch))
}

// RemoteBranch returns the last known tip of remote's branch ("" if unknown).
func (s *Store) RemoteBranch(remote, branch string) (string, error) {
	if err := CheckBranchName(remote + "/" + branch); err != nil {
		return "", err
	}
	data, err := os.ReadFile(s.remotePath(remote, branch))
	if errors.Is(err, os.ErrNotExist) {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	id := strings.TrimSpace(string(data))
	if !objects.ValidID(id) {
		return "", fmt.Errorf("remote branch %s/%s is damaged: %q", remote, branch, id)
	}
	return id, nil
}

// SetRemoteBranch records remote's branch tip; id "" deletes the record.
func (s *Store) SetRemoteBranch(remote, branch, id string) error {
	if err := CheckBranchName(remote + "/" + branch); err != nil {
		return err
	}
	path := s.remotePath(remote, branch)
	if id == "" {
		if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}
		return nil
	}
	if !objects.ValidID(id) {
		return fmt.Errorf("bad commit id %q", id)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	tmp := path + ".lock"
	if err := os.WriteFile(tmp, []byte(id+"\n"), 0o644); err != nil {
		return err
	}
	if err := os.Rename(tmp, path); err != nil {
		os.Remove(tmp)
		return err
	}
	return nil
}

// RemoteBranches returns every recorded branch of remote and its tip.
func (s *Store) RemoteBranches(remote string) (map[string]string, error) {
	root := filepath.Join(s.Dir, "refs", "remotes", remote)
	out := map[string]string{}
	err := filepath.WalkDir(root, func(p string, d os.DirEntry, err error) error {
		if err != nil {
			if errors.Is(err, os.ErrNotExist) && p == root {
				return nil
			}
			return err
		}
		if d.IsDir() || strings.HasSuffix(p, ".lock") {
			return nil
		}
		rel, err := filepath.Rel(root, p)
		if err != nil {
			return err
		}
		name := filepath.ToSlash(rel)
		id, err := s.RemoteBranch(remote, name)
		if err != nil {
			return err
		}
		out[name] = id
		return nil
	})
	return out, err
}
