package repo

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path"
	"sort"
	"strings"

	"github.com/Kirch-log/kirch/internal/index"
	"github.com/Kirch-log/kirch/internal/objects"
)

// ResetMode says how much a reset changes, like git reset.
type ResetMode int

const (
	ResetSoft  ResetMode = iota // move the branch only
	ResetMixed                  // also make the index match (default)
	ResetHard                   // also make the files match; uncommitted changes are lost
)

// Reset moves the current branch to rev and returns the commit it pointed to
// before. Mixed also resets the index, hard also the working folder.
// Untracked files are left alone unless the commit has a file at that path.
func (r *Repo) Reset(rev string, mode ResetMode) (old string, err error) {
	branch, old, err := r.Refs().Head()
	if err != nil {
		return "", err
	}
	if old == "" {
		return "", ErrNoCommits
	}
	id, err := r.Resolve(rev)
	if err != nil {
		return "", err
	}
	target, err := r.commitFiles(id)
	if err != nil {
		return "", err
	}
	switch mode {
	case ResetMixed:
		idx, err := r.LoadIndex()
		if err != nil {
			return "", err
		}
		resetIndex(idx, target, func(string) bool { return true })
		if err := idx.Save(r.IndexFile()); err != nil {
			return "", err
		}
	case ResetHard:
		if err := r.matchFiles(target); err != nil {
			return "", err
		}
	}
	if err := r.Refs().UpdateBranch(branch, id, old); err != nil {
		return "", err
	}
	// Like git, any reset ends an unfinished merge.
	return old, r.clearMergeState()
}

// ResetPaths unstages paths: their index entries are set back to rev
// ("" = HEAD), or dropped when rev doesn't have them. Files on disk and the
// branch are not touched. Returns the number of index entries changed.
func (r *Repo) ResetPaths(rev string, paths []string) (int, error) {
	target := map[string]objects.TreeEntry{}
	if rev == "" {
		_, head, err := r.Refs().Head()
		if err != nil {
			return 0, err
		}
		if head != "" {
			if target, err = r.commitFiles(head); err != nil {
				return 0, err
			}
		}
	} else {
		id, err := r.Resolve(rev)
		if err != nil {
			return 0, err
		}
		if target, err = r.commitFiles(id); err != nil {
			return 0, err
		}
	}
	idx, err := r.LoadIndex()
	if err != nil {
		return 0, err
	}
	n := resetIndex(idx, target, func(p string) bool { return underAny(p, paths) })
	if n == 0 {
		return 0, nil
	}
	return n, idx.Save(r.IndexFile())
}

// resetIndex makes the index entries for which keep(path) is true equal to
// target. Entries that already match keep their file stats; new ones get
// none, so status re-hashes those files. Returns the number changed.
func resetIndex(idx *index.Index, target map[string]objects.TreeEntry, keep func(string) bool) int {
	n := 0
	for p := range idx.Entries {
		if _, ok := target[p]; !ok && keep(p) {
			delete(idx.Entries, p)
			n++
		}
	}
	for p, t := range target {
		if !keep(p) {
			continue
		}
		if e, ok := idx.Entries[p]; ok && e.Hash == t.ID && e.Mode == t.Mode {
			continue
		}
		idx.Entries[p] = index.Entry{Hash: t.ID, Mode: t.Mode}
		n++
	}
	return n
}

func underAny(p string, paths []string) bool {
	for _, q := range paths {
		if q == "." || p == q || strings.HasPrefix(p, q+"/") {
			return true
		}
	}
	return false
}

// matchFiles makes the index and the tracked files exactly target, dropping
// any uncommitted changes. Files in the index but not in target are deleted.
func (r *Repo) matchFiles(target map[string]objects.TreeEntry) error {
	idx, err := r.LoadIndex()
	if err != nil {
		return err
	}
	indexTime := r.indexTime()
	var remove []string
	for p := range idx.Entries {
		if _, ok := target[p]; !ok {
			remove = append(remove, p)
		}
	}
	sort.Strings(remove)
	for _, p := range remove {
		if err := os.Remove(r.abs(p)); err != nil && !errors.Is(err, fs.ErrNotExist) {
			return err
		}
		r.removeEmptyDirs(path.Dir(p))
		delete(idx.Entries, p)
	}

	paths := make([]string, 0, len(target))
	for p := range target {
		paths = append(paths, p)
	}
	sort.Strings(paths)
	for _, p := range paths {
		t := target[p]
		if e, ok := idx.Entries[p]; ok && e.Hash == t.ID && e.Mode == t.Mode {
			change, err := r.worktreeChange(p, e, indexTime)
			if err != nil {
				return err
			}
			if change == Unchanged {
				continue
			}
		}
		if err := r.clearWay(p); err != nil {
			return err
		}
		e, err := r.writeFile(p, t)
		if err != nil {
			return err
		}
		idx.Entries[p] = e
	}
	return idx.Save(r.IndexFile())
}

// clearWay removes untracked things that stop a file being written at rel:
// a file where one of its folders should be, or an empty folder at rel.
func (r *Repo) clearWay(rel string) error {
	var dirs []string
	for d := path.Dir(rel); d != "."; d = path.Dir(d) {
		dirs = append(dirs, d)
	}
	for i := len(dirs) - 1; i >= 0; i-- {
		if isFile(r.abs(dirs[i])) {
			if err := os.Remove(r.abs(dirs[i])); err != nil {
				return err
			}
		}
	}
	if info, err := os.Lstat(r.abs(rel)); err == nil && info.IsDir() {
		if err := os.Remove(r.abs(rel)); err != nil {
			return fmt.Errorf("%s: a folder is in the way: %w", rel, err)
		}
	}
	return nil
}

// RevertError lists files whose later changes clash with undoing the commit
// (the same lines, or the same nets, were changed again afterwards).
type RevertError struct {
	Commit string
	Files  []string // one line per file, with wiring details for netlist.json
}

func (e *RevertError) Error() string {
	return fmt.Sprintf("can't revert %s automatically: these files were changed again in the same places:\n\t%s\n"+
		"(revert the later commits first, or edit the files by hand)", e.Commit[:7], strings.Join(e.Files, "\n\t"))
}

// ErrDirty means there are uncommitted changes to tracked files.
var ErrDirty = errors.New("you have uncommitted changes; commit them or reset --hard first")

// Revert makes a new commit that undoes the changes rev made, keeping
// everything committed after it. It is a three-way merge of "the commit's
// parent" into HEAD, with the commit itself as the base: text is undone line
// by line, netlist.json net by net. If a later commit changed the same lines
// or nets again, a *RevertError lists the files and nothing is changed.
func (r *Repo) Revert(rev string, author objects.Signature) (*CommitResult, error) {
	id, err := r.Resolve(rev)
	if err != nil {
		return nil, err
	}
	c, err := r.ReadCommit(id)
	if err != nil {
		return nil, err
	}
	if len(c.Parents) > 1 {
		return nil, fmt.Errorf("%s is a merge commit; reverting merges isn't supported yet", id[:7])
	}
	if m, err := r.Merging(); err != nil {
		return nil, err
	} else if m != nil {
		return nil, ErrMerging
	}
	st, err := r.Status()
	if err != nil {
		return nil, err
	}
	if len(st.Files) > 0 {
		return nil, ErrDirty
	}
	before := map[string]objects.TreeEntry{}
	if len(c.Parents) == 1 {
		if before, err = r.commitFiles(c.Parents[0]); err != nil {
			return nil, err
		}
	}
	after, err := r.TreeFiles(c.Tree)
	if err != nil {
		return nil, err
	}
	head, err := r.commitFiles(st.Head)
	if err != nil {
		return nil, err
	}
	if sameFiles(before, after) {
		return nil, fmt.Errorf("%s changed no files; nothing to revert", id[:7])
	}

	// Merge "before the commit" into HEAD, with the commit as the base.
	plan, err := r.planMerge(after, head, before, "parent of "+id[:7])
	if err != nil {
		return nil, err
	}
	var clashes []string
	result := map[string]objects.TreeEntry{}
	for p, f := range plan {
		switch {
		case f.conflict != nil:
			line := p
			if len(f.conflict.Details) > 0 {
				line += ": " + strings.Join(f.conflict.Details, "; ")
			}
			clashes = append(clashes, line)
		case f.entry != nil:
			result[p] = *f.entry
		}
	}
	if len(clashes) > 0 {
		sort.Strings(clashes)
		return nil, &RevertError{Commit: id, Files: clashes}
	}
	if sameFiles(head, result) {
		return nil, fmt.Errorf("the changes from %s are already undone; nothing to revert", id[:7])
	}
	if err := r.checkInTheWay(head, result); err != nil {
		return nil, err
	}

	idx, err := r.LoadIndex()
	if err != nil {
		return nil, err
	}
	paths := make([]string, 0, len(plan))
	for p := range plan {
		paths = append(paths, p)
	}
	sort.Strings(paths)
	for _, p := range paths {
		e, keep := result[p]
		h, inHead := head[p]
		switch {
		case !keep && inHead:
			if err := os.Remove(r.abs(p)); err != nil && !errors.Is(err, fs.ErrNotExist) {
				return nil, err
			}
			r.removeEmptyDirs(path.Dir(p))
			delete(idx.Entries, p)
		case keep && (!inHead || h != e):
			if err := r.clearWay(p); err != nil {
				return nil, err
			}
			written, err := r.writeFile(p, e)
			if err != nil {
				return nil, err
			}
			idx.Entries[p] = written
		}
	}
	if err := idx.Save(r.IndexFile()); err != nil {
		return nil, err
	}
	msg := fmt.Sprintf("Revert \"%s\"\n\nThis reverts commit %s.", subject(c.Message), id)
	return r.Commit(msg, author)
}

func sameFiles(a, b map[string]objects.TreeEntry) bool {
	if len(a) != len(b) {
		return false
	}
	for p, e := range a {
		if f, ok := b[p]; !ok || f.ID != e.ID || f.Mode != e.Mode {
			return false
		}
	}
	return true
}

func subject(s string) string {
	line, _, _ := strings.Cut(strings.TrimSpace(s), "\n")
	return line
}
