package repo

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"

	"github.com/Kirch-log/kirch/internal/canon"
	"github.com/Kirch-log/kirch/internal/diff"
	"github.com/Kirch-log/kirch/internal/merge"
	"github.com/Kirch-log/kirch/internal/objects"
)

// Merge state files. MERGE_HEAD and MERGE_MSG use git's names and format, so
// git sees an unfinished merge too; the conflict list is Kirch's own.
const (
	mergeHeadFile      = "MERGE_HEAD"
	mergeMsgFile       = "MERGE_MSG"
	mergeConflictsFile = "merge_conflicts.json"
)

// ErrMerging means a merge with conflicts is not finished yet.
var ErrMerging = errors.New("a merge is in progress: fix the conflicts, kirch add the files and kirch commit (or kirch merge --abort)")

// Conflict is a file the merge couldn't combine on its own.
type Conflict struct {
	Path    string
	Kind    string   // "content", "wiring", "binary", "modify/delete", "add/add"
	Details []string // for "wiring": each net or pin both sides changed differently
}

// MergeResult describes what a merge did.
type MergeResult struct {
	UpToDate    bool   // nothing to merge
	FastForward bool   // branch just moved forward to theirs
	From, To    string // branch commit before and after (To is "" with conflicts)
	Commit      *CommitResult
	Conflicts   []Conflict
}

// MergeState is an unfinished merge.
type MergeState struct {
	Head       string   // commit being merged in
	Message    string   // message for the merge commit
	Unresolved []string // conflicted files not kirch add-ed yet
}

// Merging returns the unfinished merge, or nil.
func (r *Repo) Merging() (*MergeState, error) {
	data, err := os.ReadFile(filepath.Join(r.Dir, mergeHeadFile))
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	st := &MergeState{Head: strings.TrimSpace(string(data))}
	if msg, err := os.ReadFile(filepath.Join(r.Dir, mergeMsgFile)); err == nil {
		st.Message = string(msg)
	}
	if list, err := os.ReadFile(filepath.Join(r.Dir, mergeConflictsFile)); err == nil {
		if err := json.Unmarshal(list, &st.Unresolved); err != nil {
			return nil, fmt.Errorf("%s is damaged: %w", mergeConflictsFile, err)
		}
	}
	return st, nil
}

func (r *Repo) saveMergeState(st *MergeState) error {
	if err := os.WriteFile(filepath.Join(r.Dir, mergeMsgFile), []byte(st.Message), 0o644); err != nil {
		return err
	}
	list, err := json.MarshalIndent(st.Unresolved, "", "  ")
	if err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(r.Dir, mergeConflictsFile), append(list, '\n'), 0o644); err != nil {
		return err
	}
	// MERGE_HEAD last: it is what marks a merge as in progress.
	return os.WriteFile(filepath.Join(r.Dir, mergeHeadFile), []byte(st.Head+"\n"), 0o644)
}

// clearMergeState forgets an unfinished merge (after commit, reset or abort).
func (r *Repo) clearMergeState() error {
	for _, f := range []string{mergeHeadFile, mergeMsgFile, mergeConflictsFile} {
		if err := os.Remove(filepath.Join(r.Dir, f)); err != nil && !errors.Is(err, fs.ErrNotExist) {
			return err
		}
	}
	return nil
}

// markResolved removes added paths from the unresolved conflict list.
func (r *Repo) markResolved(paths []string) error {
	st, err := r.Merging()
	if err != nil || st == nil || len(st.Unresolved) == 0 {
		return err
	}
	var left []string
	for _, p := range st.Unresolved {
		if !underAny(p, paths) {
			left = append(left, p)
		}
	}
	if len(left) == len(st.Unresolved) {
		return nil
	}
	st.Unresolved = left
	return r.saveMergeState(st)
}

// MergeAbort throws away an unfinished merge: files and index go back to
// the last commit.
func (r *Repo) MergeAbort() error {
	st, err := r.Merging()
	if err != nil {
		return err
	}
	if st == nil {
		return errors.New("no merge in progress")
	}
	_, err = r.Reset("HEAD", ResetHard)
	return err
}

// MergeBase returns the best common ancestor of two commits ("" if none):
// a common ancestor that no other common ancestor descends from; if there
// are several, the newest.
func (r *Repo) MergeBase(a, b string) (string, error) {
	ofA := map[string]bool{}
	if err := r.walkAncestors(a, func(id string, _ objects.CommitData) bool { ofA[id] = true; return true }); err != nil {
		return "", err
	}
	var common []string
	if err := r.walkAncestors(b, func(id string, _ objects.CommitData) bool {
		if ofA[id] {
			common = append(common, id)
			return false // its ancestors are common too, but not better
		}
		return true
	}); err != nil {
		return "", err
	}
	var best []string
	for _, c := range common {
		redundant := false
		for _, other := range common {
			if other != c {
				if anc, err := r.IsAncestor(c, other); err != nil {
					return "", err
				} else if anc {
					redundant = true
					break
				}
			}
		}
		if !redundant {
			best = append(best, c)
		}
	}
	if len(best) == 0 {
		return "", nil
	}
	newest, newestTime := "", int64(0)
	for _, id := range best {
		c, err := r.ReadCommit(id)
		if err != nil {
			return "", err
		}
		if t := c.Committer.When.Unix(); newest == "" || t > newestTime || t == newestTime && id < newest {
			newest, newestTime = id, t
		}
	}
	return newest, nil
}

// walkAncestors visits start and its ancestors once each; fn returns false
// to skip a commit's parents.
func (r *Repo) walkAncestors(start string, fn func(string, objects.CommitData) bool) error {
	seen := map[string]bool{start: true}
	queue := []string{start}
	for len(queue) > 0 {
		id := queue[0]
		queue = queue[1:]
		c, err := r.ReadCommit(id)
		if err != nil {
			return err
		}
		if !fn(id, c) {
			continue
		}
		for _, p := range c.Parents {
			if !seen[p] {
				seen[p] = true
				queue = append(queue, p)
			}
		}
	}
	return nil
}

// MergeMessage is git's default merge commit message.
func MergeMessage(rev, current string, isBranch bool) string {
	what := fmt.Sprintf("commit '%s'", rev)
	if isBranch {
		what = fmt.Sprintf("branch '%s'", rev)
	}
	msg := "Merge " + what
	if current != "main" && current != "master" {
		msg += " into " + current
	}
	return msg
}

// Merge joins rev into the current branch.
//
//   - rev already in the branch's history: nothing to do (UpToDate)
//   - branch is behind rev: the branch just moves forward (FastForward)
//   - otherwise each file is merged three ways against their common base:
//     text line by line, netlist.json pin by pin. With no conflicts a merge
//     commit (two parents) is made. With conflicts, merged files are written
//     with conflict markers (or a "conflicts" list in netlist.json) and the
//     merge waits for kirch add + kirch commit.
//
// Uncommitted changes to tracked files must be committed first.
func (r *Repo) Merge(rev string, author objects.Signature) (*MergeResult, error) {
	if st, err := r.Merging(); err != nil {
		return nil, err
	} else if st != nil {
		return nil, ErrMerging
	}
	theirs, err := r.Resolve(rev)
	if err != nil {
		return nil, err
	}
	status, err := r.Status()
	if err != nil {
		return nil, err
	}
	if len(status.Files) > 0 {
		return nil, ErrDirty
	}
	ours := status.Head
	res := &MergeResult{From: ours}

	if ours != "" {
		if in, err := r.IsAncestor(theirs, ours); err != nil {
			return nil, err
		} else if in {
			res.UpToDate, res.To = true, ours
			return res, nil
		}
	}
	ff := ours == ""
	if !ff {
		if ff, err = r.IsAncestor(ours, theirs); err != nil {
			return nil, err
		}
	}
	theirFiles, err := r.commitFiles(theirs)
	if err != nil {
		return nil, err
	}
	ourFiles := map[string]objects.TreeEntry{}
	if ours != "" {
		if ourFiles, err = r.commitFiles(ours); err != nil {
			return nil, err
		}
	}
	if ff {
		if err := r.checkInTheWay(ourFiles, theirFiles); err != nil {
			return nil, err
		}
		if err := r.matchFiles(theirFiles); err != nil {
			return nil, err
		}
		if err := r.Refs().UpdateBranch(status.Branch, theirs, ours); err != nil {
			return nil, err
		}
		res.FastForward, res.To = true, theirs
		return res, nil
	}

	base, err := r.MergeBase(ours, theirs)
	if err != nil {
		return nil, err
	}
	baseFiles := map[string]objects.TreeEntry{}
	if base != "" {
		if baseFiles, err = r.commitFiles(base); err != nil {
			return nil, err
		}
	}
	_, isBranch := r.branchExists(rev)
	label := rev
	plan, err := r.planMerge(baseFiles, ourFiles, theirFiles, label)
	if err != nil {
		return nil, err
	}
	result := map[string]objects.TreeEntry{}
	for p, f := range plan {
		if f.entry != nil {
			result[p] = *f.entry
		}
	}
	if err := r.checkInTheWay(ourFiles, result); err != nil {
		return nil, err
	}

	// Write the merged files and index.
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
		f := plan[p]
		switch {
		case f.conflict != nil:
			res.Conflicts = append(res.Conflicts, *f.conflict)
			if f.content != nil {
				if err := r.clearWay(p); err != nil {
					return nil, err
				}
				perm := os.FileMode(0o644)
				if f.mode == objects.ModeExec {
					perm = 0o755
				}
				if err := os.WriteFile(r.abs(p), f.content, perm); err != nil {
					return nil, err
				}
			}
			// The index keeps our version until the file is resolved and added.
		case f.entry == nil:
			if err := os.Remove(r.abs(p)); err != nil && !errors.Is(err, fs.ErrNotExist) {
				return nil, err
			}
			r.removeEmptyDirs(path.Dir(p))
			delete(idx.Entries, p)
		default:
			if o, ok := ourFiles[p]; ok && o == *f.entry {
				continue
			}
			if err := r.clearWay(p); err != nil {
				return nil, err
			}
			e, err := r.writeFile(p, *f.entry)
			if err != nil {
				return nil, err
			}
			idx.Entries[p] = e
		}
	}
	if err := idx.Save(r.IndexFile()); err != nil {
		return nil, err
	}

	st := &MergeState{Head: theirs, Message: MergeMessage(rev, status.Branch, isBranch) + "\n"}
	for _, c := range res.Conflicts {
		st.Unresolved = append(st.Unresolved, c.Path)
	}
	if err := r.saveMergeState(st); err != nil {
		return nil, err
	}
	if len(res.Conflicts) > 0 {
		return res, nil
	}
	if res.Commit, err = r.Commit(st.Message, author); err != nil {
		return nil, err
	}
	res.To = res.Commit.ID
	return res, nil
}

func (r *Repo) branchExists(name string) (string, bool) {
	id, err := r.Refs().Branch(name)
	return id, err == nil && id != ""
}

// checkInTheWay refuses when an untracked file sits where the merge would
// write a file (that file's content would be lost).
func (r *Repo) checkInTheWay(ours, result map[string]objects.TreeEntry) error {
	idx, err := r.LoadIndex()
	if err != nil {
		return err
	}
	var inWay []string
	for p, e := range result {
		if o, ok := ours[p]; ok && o == e {
			continue
		}
		if _, tracked := idx.Entries[p]; !tracked && exists(r.abs(p)) {
			inWay = append(inWay, p)
		}
	}
	if len(inWay) > 0 {
		sort.Strings(inWay)
		return &CheckoutError{Untracked: inWay}
	}
	return nil
}

// mergedFile is the outcome for one path.
type mergedFile struct {
	entry    *objects.TreeEntry // clean result; nil = deleted (or conflict)
	conflict *Conflict
	content  []byte // for conflicts: what to leave on disk (nil = leave as is)
	mode     string
}

func (r *Repo) planMerge(base, ours, theirs map[string]objects.TreeEntry, label string) (map[string]mergedFile, error) {
	all := map[string]bool{}
	for _, m := range []map[string]objects.TreeEntry{base, ours, theirs} {
		for p := range m {
			all[p] = true
		}
	}
	plan := map[string]mergedFile{}
	for p := range all {
		b, inB := base[p]
		o, inO := ours[p]
		t, inT := theirs[p]
		switch {
		case inO == inT && o == t, inB == inT && b == t:
			if inO {
				plan[p] = mergedFile{entry: &o}
			} else {
				plan[p] = mergedFile{}
			}
			continue
		case inB == inO && b == o:
			if inT {
				plan[p] = mergedFile{entry: &t}
			} else {
				plan[p] = mergedFile{}
			}
			continue
		case !inO || !inT:
			// One side deleted it, the other changed it: keep the changed one
			// on disk and let the user decide.
			kept := t
			if inO {
				kept = o
			}
			plan[p] = mergedFile{conflict: &Conflict{Path: p, Kind: "modify/delete"}, mode: kept.Mode}
			if !inO {
				data, err := r.blob(kept.ID)
				if err != nil {
					return nil, err
				}
				plan[p] = mergedFile{conflict: &Conflict{Path: p, Kind: "modify/delete"}, content: data, mode: kept.Mode}
			}
			continue
		}

		// Both sides have the file and changed it differently.
		mode := o.Mode
		if inB && b.Mode == o.Mode {
			mode = t.Mode
		}
		var bd []byte
		if inB {
			var err error
			if bd, err = r.blob(b.ID); err != nil {
				return nil, err
			}
		}
		od, err := r.blob(o.ID)
		if err != nil {
			return nil, err
		}
		td, err := r.blob(t.ID)
		if err != nil {
			return nil, err
		}
		kind := "content"
		if !inB {
			kind = "add/add"
		}
		var out []byte
		var conflict *Conflict
		switch {
		case path.Base(p) == "netlist.json":
			if data, details, err := merge.Netlist(bd, od, td); err == nil {
				out = data
				if len(details) > 0 {
					conflict = &Conflict{Path: p, Kind: "wiring", Details: details}
				}
				break
			}
			fallthrough
		case !diff.IsBinary(bd) && !diff.IsBinary(od) && !diff.IsBinary(td):
			data, n := merge.Text(bd, od, td, "HEAD", label)
			out = data
			if n > 0 {
				conflict = &Conflict{Path: p, Kind: kind}
			} else if canon.Applies(p) {
				// A clean line merge of a circuit file must still be valid.
				if formatted, err := canon.Format(path.Base(p), data); err != nil {
					conflict = &Conflict{Path: p, Kind: kind}
				} else {
					out = formatted
				}
			}
		default:
			conflict = &Conflict{Path: p, Kind: "binary"}
		}
		if conflict != nil {
			plan[p] = mergedFile{conflict: conflict, content: out, mode: mode}
			continue
		}
		id, err := r.Objects.Write(objects.Blob, out)
		if err != nil {
			return nil, err
		}
		plan[p] = mergedFile{entry: &objects.TreeEntry{Mode: mode, Name: path.Base(p), ID: id}}
	}
	return plan, nil
}

func (r *Repo) blob(id string) ([]byte, error) {
	typ, data, err := r.Objects.Read(id)
	if err != nil {
		return nil, err
	}
	if typ != objects.Blob {
		return nil, fmt.Errorf("%s is a %s, not a file", id, typ)
	}
	return data, nil
}
