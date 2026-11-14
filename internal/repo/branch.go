package repo

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"

	"github.com/Kirch-log/kirch/internal/index"
	"github.com/Kirch-log/kirch/internal/objects"
	"github.com/Kirch-log/kirch/internal/refs"
)

// ErrNotMerged means a branch has commits that the current branch doesn't.
var ErrNotMerged = errors.New("branch is not merged into the current branch")

// CreateBranch makes a new branch pointing at rev ("" = HEAD).
func (r *Repo) CreateBranch(name, rev string) (string, error) {
	if err := refs.CheckBranchName(name); err != nil {
		return "", err
	}
	branches, err := r.Refs().Branches()
	if err != nil {
		return "", err
	}
	for b := range branches {
		if b == name {
			return "", fmt.Errorf("a branch named %q already exists", name)
		}
		if strings.HasPrefix(b, name+"/") || strings.HasPrefix(name, b+"/") {
			return "", fmt.Errorf("can't create %q while branch %q exists", name, b)
		}
	}
	id, err := r.Resolve(rev)
	if err != nil {
		return "", err
	}
	return id, r.Refs().UpdateBranch(name, id, "")
}

// DeleteBranch removes a branch. Unless force is set, it refuses when the
// branch has commits not reachable from HEAD (they would be lost).
func (r *Repo) DeleteBranch(name string, force bool) (string, error) {
	current, head, err := r.Refs().Head()
	if err != nil {
		return "", err
	}
	if name == current {
		return "", fmt.Errorf("can't delete %q: it is the current branch", name)
	}
	id, err := r.Refs().Branch(name)
	if err != nil {
		return "", err
	}
	if id == "" {
		return "", fmt.Errorf("branch %q not found", name)
	}
	if !force {
		merged := false
		if head != "" {
			if merged, err = r.IsAncestor(id, head); err != nil {
				return "", err
			}
		}
		if !merged {
			return "", fmt.Errorf("%w: %s (use -D to delete it anyway)", ErrNotMerged, name)
		}
	}
	return id, r.Refs().DeleteBranch(name, id)
}

// IsAncestor reports whether commit a is reachable from commit b (or is b).
func (r *Repo) IsAncestor(a, b string) (bool, error) {
	seen := map[string]bool{b: true}
	stack := []string{b}
	for len(stack) > 0 {
		id := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		if id == a {
			return true, nil
		}
		c, err := r.ReadCommit(id)
		if err != nil {
			return false, err
		}
		for _, p := range c.Parents {
			if !seen[p] {
				seen[p] = true
				stack = append(stack, p)
			}
		}
	}
	return false, nil
}

// CheckoutError lists the files that stop a checkout.
type CheckoutError struct {
	Changed   []string // have uncommitted changes and differ between the branches
	Untracked []string // untracked files the other branch would overwrite
}

func (e *CheckoutError) Error() string {
	var b strings.Builder
	if len(e.Changed) > 0 {
		b.WriteString("your changes to these files would be overwritten (commit them first):\n\t" + strings.Join(e.Changed, "\n\t"))
	}
	if len(e.Untracked) > 0 {
		if b.Len() > 0 {
			b.WriteString("\n")
		}
		b.WriteString("these untracked files would be overwritten (move or add them first):\n\t" + strings.Join(e.Untracked, "\n\t"))
	}
	return b.String()
}

// Checkout switches to another branch: files and index are updated to its
// last commit, then HEAD moves. Like git, uncommitted changes are kept when
// the file is the same on both branches; if a changed file differs between
// the branches, or an untracked file is in the way, nothing is touched and a
// *CheckoutError is returned.
func (r *Repo) Checkout(branch string) error {
	current, from, err := r.Refs().Head()
	if err != nil {
		return err
	}
	to, err := r.Refs().Branch(branch)
	if err != nil {
		return err
	}
	if to == "" {
		return fmt.Errorf("branch %q not found", branch)
	}
	if branch == current {
		return nil
	}

	oldFiles := map[string]objects.TreeEntry{}
	if from != "" {
		if oldFiles, err = r.commitFiles(from); err != nil {
			return err
		}
	}
	newFiles, err := r.commitFiles(to)
	if err != nil {
		return err
	}
	st, err := r.Status()
	if err != nil {
		return err
	}
	idx, err := r.LoadIndex()
	if err != nil {
		return err
	}
	dirty := map[string]bool{}
	for _, f := range st.Files {
		dirty[f.Path] = true
	}

	// Paths that differ between the two commits.
	var update, remove []string
	for p, n := range newFiles {
		if o, ok := oldFiles[p]; !ok || o.ID != n.ID || o.Mode != n.Mode {
			update = append(update, p)
		}
	}
	for p := range oldFiles {
		if _, ok := newFiles[p]; !ok {
			remove = append(remove, p)
		}
	}
	sort.Strings(update)
	sort.Strings(remove)

	// Check everything before changing anything.
	conflict := &CheckoutError{}
	removing := map[string]bool{}
	for _, p := range remove {
		removing[p] = true
		if dirty[p] {
			conflict.Changed = append(conflict.Changed, p)
		}
	}
	for _, p := range update {
		if dirty[p] {
			conflict.Changed = append(conflict.Changed, p)
			continue
		}
		if _, tracked := idx.Entries[p]; !tracked && exists(r.abs(p)) {
			conflict.Untracked = append(conflict.Untracked, p)
		}
		for d := path.Dir(p); d != "."; d = path.Dir(d) {
			if _, tracked := idx.Entries[d]; !tracked && isFile(r.abs(d)) {
				conflict.Untracked = append(conflict.Untracked, d)
			}
		}
	}
	if len(conflict.Changed) > 0 || len(conflict.Untracked) > 0 {
		sort.Strings(conflict.Changed)
		sort.Strings(conflict.Untracked)
		return conflict
	}

	// Removals first, so a file can turn into a folder.
	for _, p := range remove {
		if err := os.Remove(r.abs(p)); err != nil && !errors.Is(err, fs.ErrNotExist) {
			return err
		}
		r.removeEmptyDirs(path.Dir(p))
		delete(idx.Entries, p)
	}
	for _, p := range update {
		e, err := r.writeFile(p, newFiles[p])
		if err != nil {
			return err
		}
		idx.Entries[p] = e
	}
	if err := idx.Save(r.IndexFile()); err != nil {
		return err
	}
	return r.Refs().SetHead(branch)
}

func (r *Repo) commitFiles(id string) (map[string]objects.TreeEntry, error) {
	c, err := r.ReadCommit(id)
	if err != nil {
		return nil, err
	}
	return r.TreeFiles(c.Tree)
}

// writeFile writes a blob to the working folder and returns its index entry.
func (r *Repo) writeFile(rel string, e objects.TreeEntry) (index.Entry, error) {
	typ, data, err := r.Objects.Read(e.ID)
	if err != nil {
		return index.Entry{}, err
	}
	if typ != objects.Blob {
		return index.Entry{}, fmt.Errorf("%s: %s is a %s, not a file", rel, e.ID, typ)
	}
	abs := r.abs(rel)
	if err := os.MkdirAll(filepath.Dir(abs), 0o755); err != nil {
		return index.Entry{}, err
	}
	perm := os.FileMode(0o644)
	if e.Mode == objects.ModeExec {
		perm = 0o755
	}
	// Remove first so a changed mode takes effect.
	if err := os.Remove(abs); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return index.Entry{}, err
	}
	if err := os.WriteFile(abs, data, perm); err != nil {
		return index.Entry{}, err
	}
	info, err := os.Stat(abs)
	if err != nil {
		return index.Entry{}, err
	}
	return index.Entry{Hash: e.ID, Mode: e.Mode, Size: info.Size(), MtimeNs: info.ModTime().UnixNano()}, nil
}

// removeEmptyDirs deletes dir and its parents while they are empty.
func (r *Repo) removeEmptyDirs(dir string) {
	for ; dir != "."; dir = path.Dir(dir) {
		if os.Remove(r.abs(dir)) != nil {
			return
		}
	}
}

func exists(p string) bool {
	_, err := os.Lstat(p)
	return err == nil
}

func isFile(p string) bool {
	info, err := os.Lstat(p)
	return err == nil && !info.IsDir()
}
