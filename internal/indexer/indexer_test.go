package indexer

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/Kirch-log/kirch/internal/db"
	"github.com/Kirch-log/kirch/internal/db/dbtest"
	"github.com/Kirch-log/kirch/internal/objects"
	"github.com/Kirch-log/kirch/internal/repo"
)

func TestMain(m *testing.M) { dbtest.Main(m) }

// folders is a minimal Opener over one base folder: <base>/<owner>/<name>.
type folders string

func (f folders) Open(owner, name string) (*repo.Repo, error) {
	return repo.Open(filepath.Join(string(f), owner, name))
}

func (f folders) List() ([][2]string, error) {
	owners, _ := os.ReadDir(string(f))
	var out [][2]string
	for _, o := range owners {
		names, _ := os.ReadDir(filepath.Join(string(f), o.Name()))
		for _, n := range names {
			out = append(out, [2]string{o.Name(), n.Name()})
		}
	}
	return out, nil
}

type fixture struct {
	t     *testing.T
	d     *db.DB
	repos folders
	r     *repo.Repo
	n     int
}

func setup(t *testing.T) *fixture {
	d := dbtest.New(t)
	base := t.TempDir()
	r, err := repo.Init(filepath.Join(base, "evin", "servo-board"))
	if err != nil {
		t.Fatal(err)
	}
	return &fixture{t: t, d: d, repos: folders(base), r: r}
}

func (f *fixture) commit(file, content, msg string, zone *time.Location) string {
	f.t.Helper()
	if err := os.WriteFile(filepath.Join(f.r.Root, file), []byte(content), 0o644); err != nil {
		f.t.Fatal(err)
	}
	if _, err := f.r.Add([]string{f.r.Root}); err != nil {
		f.t.Fatal(err)
	}
	f.n++
	if zone == nil {
		zone = time.UTC
	}
	who := objects.Signature{Name: "Evin", Email: "evin@example.com", When: time.Unix(1700000000+int64(f.n)*60, 0).In(zone)}
	res, err := f.r.Commit(msg, who)
	if err != nil {
		f.t.Fatal(err)
	}
	return res.ID
}

func (f *fixture) index() Stats {
	f.t.Helper()
	st, err := IndexRepo(context.Background(), f.d, f.repos, "evin", "servo-board")
	if err != nil {
		f.t.Fatal(err)
	}
	return st
}

func (f *fixture) repoRow() db.Repo {
	f.t.Helper()
	r, err := f.d.GetRepo(context.Background(), "evin", "servo-board")
	if err != nil {
		f.t.Fatal(err)
	}
	return r
}

func TestIndexIsIdempotent(t *testing.T) {
	f := setup(t)
	f.commit("a.txt", "1", "first", nil)
	f.commit("a.txt", "2", "second", nil)
	if st := f.index(); st.NewCommits != 2 || !st.BranchesMoved {
		t.Fatalf("first index: %+v", st)
	}
	before := f.repoRow()
	if st := f.index(); st.NewCommits != 0 || st.BranchesMoved {
		t.Fatalf("second index changed something: %+v", st)
	}
	after := f.repoRow()
	if after.Commits != 2 || after.Branches != 1 || !after.UpdatedAt.Equal(before.UpdatedAt) {
		t.Fatalf("rows changed on re-index: before %+v after %+v", before, after)
	}
	if after.LastCommit == nil || after.LastCommit.Summary != "second" {
		t.Fatalf("last commit: %+v", after.LastCommit)
	}
}

func TestIndexNewCommitsAndHistoryOrder(t *testing.T) {
	f := setup(t)
	f.commit("a.txt", "1", "one", nil)
	f.index()
	f.commit("a.txt", "2", "two", nil)
	f.commit("a.txt", "3", "three\n\nwith a body", nil)
	if st := f.index(); st.NewCommits != 2 || !st.BranchesMoved {
		t.Fatalf("got %+v", st)
	}
	row := f.repoRow()
	tip, err := f.d.BranchTip(context.Background(), row.ID, "main")
	if err != nil {
		t.Fatal(err)
	}
	hist, err := f.d.History(context.Background(), row.ID, tip, 10, 0)
	if err != nil {
		t.Fatal(err)
	}
	want, err := f.r.Log("main", 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(hist) != len(want) {
		t.Fatalf("history has %d commits, kirch log %d", len(hist), len(want))
	}
	for i := range want {
		if hist[i].ID != want[i].ID {
			t.Fatalf("history[%d] = %s, kirch log has %s", i, hist[i].ID[:7], want[i].ID[:7])
		}
	}
	if hist[0].Summary != "three" || hist[0].Message != "three\n\nwith a body\n" {
		t.Fatalf("summary/message: %q / %q", hist[0].Summary, hist[0].Message)
	}
	page2, _ := f.d.History(context.Background(), row.ID, tip, 2, 2)
	if len(page2) != 1 || page2[0].ID != want[2].ID {
		t.Fatalf("paging: %+v", page2)
	}
}

func TestIndexMergeAndDeletedBranch(t *testing.T) {
	f := setup(t)
	base := f.commit("a.txt", "1", "base", nil)
	if _, err := f.r.CreateBranch("feature", ""); err != nil {
		t.Fatal(err)
	}
	f.commit("a.txt", "2", "on main", nil)
	if err := f.r.Checkout("feature"); err != nil {
		t.Fatal(err)
	}
	feat := f.commit("b.txt", "x", "on feature", nil)
	if err := f.r.Checkout("main"); err != nil {
		t.Fatal(err)
	}
	res, err := f.r.Merge("feature", objects.Signature{Name: "E", Email: "e@x", When: time.Unix(1800000000, 0).UTC()})
	if err != nil || len(res.Conflicts) > 0 {
		t.Fatalf("merge: %+v %v", res, err)
	}
	f.index()
	row := f.repoRow()
	if row.Commits != 4 || row.Branches != 2 {
		t.Fatalf("got %d commits, %d branches", row.Commits, row.Branches)
	}
	m, err := f.d.GetCommit(context.Background(), row.ID, res.To[:8])
	if err != nil {
		t.Fatal(err)
	}
	if len(m.Parents) != 2 || m.Parents[1] != feat {
		t.Fatalf("merge parents: %v", m.Parents)
	}
	hist, _ := f.d.History(context.Background(), row.ID, res.To, 100, 0)
	if len(hist) != 4 || hist[len(hist)-1].ID != base {
		t.Fatalf("history through merge: %d commits", len(hist))
	}

	// Delete the feature branch: its row goes, commits stay.
	if err := f.r.Refs().DeleteBranch("feature", feat); err != nil {
		t.Fatal(err)
	}
	if st := f.index(); !st.BranchesMoved || st.BranchesStored != 1 {
		t.Fatalf("after delete: %+v", st)
	}
	bs, _ := f.d.Branches(context.Background(), row.ID)
	if len(bs) != 1 || bs[0].Name != "main" {
		t.Fatalf("branches: %+v", bs)
	}
}

func TestIndexKeepsTimeZones(t *testing.T) {
	f := setup(t)
	ist := time.FixedZone("", 5*3600+30*60)
	id := f.commit("a.txt", "1", "from India", ist)
	f.index()
	c, err := f.d.GetCommit(context.Background(), f.repoRow().ID, id)
	if err != nil {
		t.Fatal(err)
	}
	if _, off := c.Author.When.Zone(); off != 5*3600+30*60 {
		t.Fatalf("author zone offset %d", off)
	}
	orig, _ := f.r.ReadCommit(id)
	if !c.Author.When.Equal(orig.Author.When) {
		t.Fatalf("time %v != %v", c.Author.When, orig.Author.When)
	}
}

func TestGetCommitLookups(t *testing.T) {
	f := setup(t)
	id := f.commit("a.txt", "1", "one", nil)
	f.index()
	ctx, rid := context.Background(), f.repoRow().ID
	for _, bad := range []string{"", "abc", "zzzz", "0000000000", id + "0"} {
		if _, err := f.d.GetCommit(ctx, rid, bad); !errors.Is(err, db.ErrNotFound) {
			t.Errorf("%q: got %v, want ErrNotFound", bad, err)
		}
	}
	for _, good := range []string{id, id[:4], id[:12]} {
		if c, err := f.d.GetCommit(ctx, rid, good); err != nil || c.ID != id {
			t.Errorf("%q: %v", good, err)
		}
	}
	if _, err := f.d.GetRepo(ctx, "evin", "nope"); !errors.Is(err, db.ErrNotFound) {
		t.Errorf("unknown repo: %v", err)
	}
}

func TestConcurrentIndexing(t *testing.T) {
	f := setup(t)
	for i := 0; i < 20; i++ {
		f.commit("a.txt", fmt.Sprint(i), fmt.Sprint("c", i), nil)
	}
	var wg sync.WaitGroup
	errs := make(chan error, 8)
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, err := IndexRepo(context.Background(), f.d, f.repos, "evin", "servo-board")
			errs <- err
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}
	if row := f.repoRow(); row.Commits != 20 {
		t.Fatalf("got %d commits, want 20", row.Commits)
	}
}

func TestIndexAllAndMigrateTwice(t *testing.T) {
	f := setup(t)
	f.commit("a.txt", "1", "one", nil)
	_, err := repo.Init(filepath.Join(string(f.repos), "hb", "probe"))
	if err != nil {
		t.Fatal(err)
	}
	n, err := IndexAll(context.Background(), f.d, f.repos)
	if err != nil || n != 2 {
		t.Fatalf("IndexAll: %d, %v", n, err)
	}
	repos, err := f.d.ListRepos(context.Background())
	if err != nil || len(repos) != 2 {
		t.Fatalf("ListRepos: %+v %v", repos, err)
	}
	if err := f.d.Migrate(context.Background()); err != nil {
		t.Fatalf("second migrate: %v", err)
	}
}

// Commits made in the same second must come out in kirch log's order, not
// sorted by id.
func TestHistoryTiesMatchKirchLog(t *testing.T) {
	f := setup(t)
	who := objects.Signature{Name: "E", Email: "e@x", When: time.Unix(1700000000, 0).UTC()}
	mk := func(content, msg string) {
		os.WriteFile(filepath.Join(f.r.Root, "a.txt"), []byte(content), 0o644)
		f.r.Add([]string{f.r.Root})
		if _, err := f.r.Commit(msg, who); err != nil {
			t.Fatal(err)
		}
	}
	mk("0", "base")
	f.r.CreateBranch("side", "")
	for i := 0; i < 4; i++ {
		mk(fmt.Sprint("m", i), fmt.Sprint("main ", i))
	}
	f.r.Checkout("side")
	for i := 0; i < 4; i++ {
		os.WriteFile(filepath.Join(f.r.Root, "b.txt"), []byte(fmt.Sprint(i)), 0o644)
		f.r.Add([]string{f.r.Root})
		f.r.Commit(fmt.Sprint("side ", i), who)
	}
	f.r.Checkout("main")
	if _, err := f.r.Merge("side", who); err != nil {
		t.Fatal(err)
	}
	f.index()
	want, _ := f.r.Log("main", 0)
	row := f.repoRow()
	tip, _ := f.d.BranchTip(context.Background(), row.ID, "main")
	for _, pageSize := range []int{100, 3} {
		var got []string
		for off := 0; off < len(want); off += pageSize {
			page, err := f.d.History(context.Background(), row.ID, tip, pageSize, off)
			if err != nil {
				t.Fatal(err)
			}
			for _, c := range page {
				got = append(got, c.ID)
			}
		}
		if len(got) != len(want) {
			t.Fatalf("page %d: %d commits, want %d", pageSize, len(got), len(want))
		}
		for i := range want {
			if got[i] != want[i].ID {
				t.Fatalf("page %d: position %d is %s, kirch log has %s", pageSize, i, got[i][:7], want[i].ID[:7])
			}
		}
	}
}
