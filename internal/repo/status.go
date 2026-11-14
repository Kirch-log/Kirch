package repo

import (
	"errors"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"sort"

	"github.com/Kirch-log/kirch/internal/index"
	"github.com/Kirch-log/kirch/internal/objects"
)

// Change kinds, same letters as git status --porcelain.
const (
	Unchanged = ' '
	Added     = 'A'
	Modified  = 'M'
	Deleted   = 'D'
)

// FileStatus is one tracked file that differs somewhere.
type FileStatus struct {
	Path     string
	Staged   byte // last commit → index (what the next commit will change)
	Unstaged byte // index → working folder (not staged yet)
}

// Status is the state of the working folder.
type Status struct {
	Branch    string
	Head      string       // "" when the branch has no commits yet
	Files     []FileStatus // sorted by path
	Untracked []string     // sorted; a folder with nothing tracked in it is "dir/"
}

// Clean reports whether there is nothing to commit and nothing untracked.
func (s *Status) Clean() bool { return len(s.Files) == 0 && len(s.Untracked) == 0 }

// HasStaged reports whether the next commit would change anything.
func (s *Status) HasStaged() bool {
	for _, f := range s.Files {
		if f.Staged != Unchanged {
			return true
		}
	}
	return false
}

// Status compares the last commit, the index and the working folder.
//
// Circuit files are compared in canonical form (the form kirch add would
// stage), so a file that was only reformatted is not reported as modified.
func (r *Repo) Status() (*Status, error) {
	branch, head, err := r.Refs().Head()
	if err != nil {
		return nil, err
	}
	committed := map[string]objects.TreeEntry{}
	if head != "" {
		c, err := r.ReadCommit(head)
		if err != nil {
			return nil, err
		}
		if committed, err = r.TreeFiles(c.Tree); err != nil {
			return nil, err
		}
	}
	idx, err := r.LoadIndex()
	if err != nil {
		return nil, err
	}
	// Files changed after the index was saved may share its mtime; those are
	// always re-hashed (git calls them "racily clean").
	var indexTime int64
	if info, err := os.Stat(r.IndexFile()); err == nil {
		indexTime = info.ModTime().UnixNano()
	}

	byPath := map[string]*FileStatus{}
	get := func(p string) *FileStatus {
		if byPath[p] == nil {
			byPath[p] = &FileStatus{Path: p, Staged: Unchanged, Unstaged: Unchanged}
		}
		return byPath[p]
	}
	for p, e := range idx.Entries {
		if c, ok := committed[p]; !ok {
			get(p).Staged = Added
		} else if c.ID != e.Hash || c.Mode != e.Mode {
			get(p).Staged = Modified
		}
		change, err := r.worktreeChange(p, e, indexTime)
		if err != nil {
			return nil, err
		}
		if change != Unchanged {
			get(p).Unstaged = change
		}
	}
	for p := range committed {
		if _, ok := idx.Entries[p]; !ok {
			get(p).Staged = Deleted
		}
	}

	st := &Status{Branch: branch, Head: head}
	for _, f := range byPath {
		st.Files = append(st.Files, *f)
	}
	sort.Slice(st.Files, func(i, j int) bool { return st.Files[i].Path < st.Files[j].Path })
	if st.Untracked, err = r.untracked(idx); err != nil {
		return nil, err
	}
	return st, nil
}

// worktreeChange compares a staged file with the file on disk.
func (r *Repo) worktreeChange(rel string, e index.Entry, indexTime int64) (byte, error) {
	abs := r.abs(rel)
	info, err := os.Lstat(abs)
	if errors.Is(err, fs.ErrNotExist) || (err == nil && !info.Mode().IsRegular()) {
		return Deleted, nil
	}
	if err != nil {
		return 0, err
	}
	mtime := info.ModTime().UnixNano()
	if info.Size() == e.Size && mtime == e.MtimeNs && mtime < indexTime {
		return Unchanged, nil
	}
	p, err := prepare(abs, rel)
	if err != nil {
		// An invalid circuit file can't be the staged one.
		return Modified, nil
	}
	if objects.Hash(objects.Blob, p.content) != e.Hash || p.mode != e.Mode {
		return Modified, nil
	}
	return Unchanged, nil
}

// untracked lists files on disk that aren't in the index. A folder holding
// no tracked files is listed once as "dir/" instead of file by file.
func (r *Repo) untracked(idx *index.Index) ([]string, error) {
	trackedDirs := map[string]bool{}
	for p := range idx.Entries {
		for d := path.Dir(p); d != "."; d = path.Dir(d) {
			trackedDirs[d] = true
		}
	}
	seen := map[string]bool{}
	var out []string
	err := r.walk(r.Root, func(_, rel string) error {
		if _, ok := idx.Entries[rel]; ok {
			return nil
		}
		show := rel
		for d := path.Dir(rel); d != "."; d = path.Dir(d) {
			if !trackedDirs[d] {
				show = d + "/"
			}
		}
		if !seen[show] {
			seen[show] = true
			out = append(out, show)
		}
		return nil
	})
	sort.Strings(out)
	return out, err
}

// TreeFiles lists every file in a tree, with slash paths, recursing into folders.
func (r *Repo) TreeFiles(treeID string) (map[string]objects.TreeEntry, error) {
	out := map[string]objects.TreeEntry{}
	var walk func(id, prefix string) error
	walk = func(id, prefix string) error {
		typ, data, err := r.Objects.Read(id)
		if err != nil {
			return err
		}
		if typ != objects.Tree {
			return errors.New(id + " is a " + string(typ) + ", not a tree")
		}
		entries, err := objects.ParseTree(data)
		if err != nil {
			return err
		}
		for _, e := range entries {
			if e.IsDir() {
				if err := walk(e.ID, prefix+e.Name+"/"); err != nil {
					return err
				}
				continue
			}
			out[prefix+e.Name] = e
		}
		return nil
	}
	return out, walk(treeID, "")
}

func (r *Repo) abs(rel string) string {
	return filepath.Join(r.Root, filepath.FromSlash(rel))
}
