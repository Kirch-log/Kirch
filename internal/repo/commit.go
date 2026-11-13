package repo

import (
	"errors"
	"fmt"
	"strings"

	"github.com/Kirch-log/kirch/internal/index"
	"github.com/Kirch-log/kirch/internal/objects"
	"github.com/Kirch-log/kirch/internal/refs"
)

// ErrNothingToCommit means the staged files are the same as the last commit.
var ErrNothingToCommit = errors.New("nothing to commit: staged files match the last commit")

// Refs returns the branch pointers of this repository.
func (r *Repo) Refs() *refs.Store { return refs.New(r.Dir) }

// CommitResult describes a new commit.
type CommitResult struct {
	ID     string
	Branch string
	Parent string // "" for the first commit on the branch
	Tree   string
}

// Commit snapshots the staged files as a new commit on the current branch.
// Author is also used as committer.
func (r *Repo) Commit(message string, author objects.Signature) (*CommitResult, error) {
	message = strings.TrimSpace(message)
	if message == "" {
		return nil, errors.New("commit message is empty")
	}
	idx, err := r.LoadIndex()
	if err != nil {
		return nil, err
	}
	if len(idx.Entries) == 0 {
		return nil, errors.New("nothing to commit (stage files with kirch add)")
	}

	branch, parent, err := r.Refs().Head()
	if err != nil {
		return nil, err
	}
	tree, err := r.WriteTree(idx)
	if err != nil {
		return nil, err
	}
	var parents []string
	if parent != "" {
		prev, err := r.ReadCommit(parent)
		if err != nil {
			return nil, err
		}
		if prev.Tree == tree {
			return nil, ErrNothingToCommit
		}
		parents = []string{parent}
	}

	data, err := objects.EncodeCommit(objects.CommitData{
		Tree:      tree,
		Parents:   parents,
		Author:    author,
		Committer: author,
		Message:   message + "\n",
	})
	if err != nil {
		return nil, err
	}
	id, err := r.Objects.Write(objects.Commit, data)
	if err != nil {
		return nil, err
	}
	if err := r.Refs().UpdateBranch(branch, id, parent); err != nil {
		return nil, err
	}
	return &CommitResult{ID: id, Branch: branch, Parent: parent, Tree: tree}, nil
}

// ReadCommit loads and decodes a commit object.
func (r *Repo) ReadCommit(id string) (objects.CommitData, error) {
	typ, data, err := r.Objects.Read(id)
	if err != nil {
		return objects.CommitData{}, err
	}
	if typ != objects.Commit {
		return objects.CommitData{}, fmt.Errorf("%s is a %s, not a commit", id, typ)
	}
	return objects.ParseCommit(data)
}

// WriteTree stores the staged files as nested tree objects and returns the
// id of the top tree (same as git write-tree).
func (r *Repo) WriteTree(idx *index.Index) (string, error) {
	root := &dirNode{files: map[string]index.Entry{}, dirs: map[string]*dirNode{}}
	for _, p := range idx.Paths() {
		parts := strings.Split(p, "/")
		node := root
		for _, dir := range parts[:len(parts)-1] {
			child, ok := node.dirs[dir]
			if !ok {
				child = &dirNode{files: map[string]index.Entry{}, dirs: map[string]*dirNode{}}
				node.dirs[dir] = child
			}
			node = child
		}
		name := parts[len(parts)-1]
		if _, clash := node.dirs[name]; clash {
			return "", fmt.Errorf("%s is both a file and a folder in the index", p)
		}
		node.files[name] = idx.Entries[p]
	}
	return r.writeDir(root)
}

type dirNode struct {
	files map[string]index.Entry
	dirs  map[string]*dirNode
}

func (r *Repo) writeDir(node *dirNode) (string, error) {
	var entries []objects.TreeEntry
	for name, e := range node.files {
		if _, clash := node.dirs[name]; clash {
			return "", fmt.Errorf("%s is both a file and a folder in the index", name)
		}
		entries = append(entries, objects.TreeEntry{Mode: e.Mode, Name: name, ID: e.Hash})
	}
	for name, child := range node.dirs {
		id, err := r.writeDir(child)
		if err != nil {
			return "", err
		}
		entries = append(entries, objects.TreeEntry{Mode: objects.ModeDir, Name: name, ID: id})
	}
	data, err := objects.EncodeTree(entries)
	if err != nil {
		return "", err
	}
	return r.Objects.Write(objects.Tree, data)
}
