package repo

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"sort"
	"strings"

	"github.com/Kirch-log/kirch/internal/diff"
	"github.com/Kirch-log/kirch/internal/index"
	"github.com/Kirch-log/kirch/internal/objects"
)

// version is one side's copy of a file. Files read from disk carry their
// data; others are loaded from the object store when needed.
type version struct {
	id, mode string
	data     []byte
	onDisk   bool
}

type side map[string]version

// DiffWorktree compares the working folder with the index (rev == "", like
// git diff) or with a commit (like git diff <rev>). Only tracked files are compared.
func (r *Repo) DiffWorktree(rev string) ([]diff.File, error) {
	idx, err := r.LoadIndex()
	if err != nil {
		return nil, err
	}
	old := side{}
	if rev == "" {
		old = indexSide(idx)
	} else if old, err = r.commitSide(rev); err != nil {
		return nil, err
	}
	work, err := r.worktreeSide(idx)
	if err != nil {
		return nil, err
	}
	return r.compare(old, work)
}

// DiffStaged compares the last commit with the index (git diff --staged).
func (r *Repo) DiffStaged() ([]diff.File, error) {
	idx, err := r.LoadIndex()
	if err != nil {
		return nil, err
	}
	old := side{}
	if _, head, err := r.Refs().Head(); err != nil {
		return nil, err
	} else if head != "" {
		if old, err = r.commitSide(head); err != nil {
			return nil, err
		}
	}
	return r.compare(old, indexSide(idx))
}

// DiffCommits compares two commits (git diff <a> <b>).
func (r *Repo) DiffCommits(a, b string) ([]diff.File, error) {
	old, err := r.commitSide(a)
	if err != nil {
		return nil, err
	}
	new, err := r.commitSide(b)
	if err != nil {
		return nil, err
	}
	return r.compare(old, new)
}

func indexSide(idx *index.Index) side {
	s := side{}
	for p, e := range idx.Entries {
		s[p] = version{id: e.Hash, mode: e.Mode}
	}
	return s
}

func (r *Repo) commitSide(rev string) (side, error) {
	id, err := r.Resolve(rev)
	if err != nil {
		return nil, err
	}
	files, err := r.commitFiles(id)
	if err != nil {
		return nil, err
	}
	s := side{}
	for p, e := range files {
		s[p] = version{id: e.ID, mode: e.Mode}
	}
	return s, nil
}

// worktreeSide reads every tracked file on disk; missing files are left out.
func (r *Repo) worktreeSide(idx *index.Index) (side, error) {
	indexTime := r.indexTime()
	s := side{}
	for p, e := range idx.Entries {
		v, err := r.worktreeFile(p, e, indexTime)
		if err != nil {
			return nil, err
		}
		if v != nil {
			s[p] = *v
		}
	}
	return s, nil
}

// worktreeFile returns the on-disk version of a tracked file as kirch add
// would stage it (circuit files in canonical form), or nil if it's gone.
// Files whose size and mtime match the index entry aren't re-read.
func (r *Repo) worktreeFile(rel string, e index.Entry, indexTime int64) (*version, error) {
	abs := r.abs(rel)
	info, err := os.Lstat(abs)
	if errors.Is(err, fs.ErrNotExist) || (err == nil && !info.Mode().IsRegular()) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	mtime := info.ModTime().UnixNano()
	if info.Size() == e.Size && mtime == e.MtimeNs && mtime < indexTime {
		return &version{id: e.Hash, mode: e.Mode}, nil
	}
	p, err := prepare(abs, rel)
	if err != nil {
		// An invalid circuit file: compare its raw bytes.
		data, rerr := os.ReadFile(abs)
		if rerr != nil {
			return nil, rerr
		}
		p = pending{content: data, mode: index.ModeFile}
		if info.Mode().Perm()&0o111 != 0 {
			p.mode = index.ModeExec
		}
	}
	return &version{id: objects.Hash(objects.Blob, p.content), mode: p.mode, data: p.content, onDisk: true}, nil
}

func (r *Repo) indexTime() int64 {
	if info, err := os.Stat(r.IndexFile()); err == nil {
		return info.ModTime().UnixNano()
	}
	return 0
}

// compare pairs up the two sides and loads the contents of changed files.
func (r *Repo) compare(old, new side) ([]diff.File, error) {
	paths := map[string]bool{}
	for p := range old {
		paths[p] = true
	}
	for p := range new {
		paths[p] = true
	}
	var out []diff.File
	for p := range paths {
		o, hasOld := old[p]
		n, hasNew := new[p]
		if hasOld && hasNew && o.id == n.id && o.mode == n.mode {
			continue
		}
		f := diff.File{Path: p}
		var err error
		if hasOld {
			f.OldID, f.OldMode = o.id, o.mode
			if f.Old, err = r.load(o); err != nil {
				return nil, err
			}
		}
		if hasNew {
			f.NewID, f.NewMode = n.id, n.mode
			if f.New, err = r.load(n); err != nil {
				return nil, err
			}
		}
		out = append(out, f)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Path < out[j].Path })
	return out, nil
}

func (r *Repo) load(v version) ([]byte, error) {
	if v.onDisk {
		return v.data, nil
	}
	typ, data, err := r.Objects.Read(v.id)
	if err != nil {
		return nil, err
	}
	if typ != objects.Blob {
		return nil, fmt.Errorf("%s is a %s, not a file", v.id, typ)
	}
	return data, nil
}

// RelPath turns a command-line path into a repo-relative slash path ("."
// for the repository root).
func (r *Repo) RelPath(arg string) (string, error) {
	_, rel, err := r.resolve(arg)
	return rel, err
}

// FilterPaths keeps the files at or under any of the repo-relative paths.
func FilterPaths(files []diff.File, paths []string) []diff.File {
	if len(paths) == 0 {
		return files
	}
	var out []diff.File
	for _, f := range files {
		for _, p := range paths {
			if p == "." || f.Path == p || strings.HasPrefix(f.Path, p+"/") {
				out = append(out, f)
				break
			}
		}
	}
	return out
}
