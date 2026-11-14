package repo

import (
	"os"
	"os/exec"
	"strings"
	"testing"
)

// Oracle: HEAD~N / ^N resolve to the same commits as git rev-parse.
func TestResolveStepsMatchGit(t *testing.T) {
	git, err := exec.LookPath("git")
	if err != nil {
		t.Skip("git not installed")
	}
	r, _ := forked(t)
	put(t, r, "a.txt", "main 1\n")
	commitIn(t, r, "main 1")
	r.Checkout("side")
	put(t, r, "b.txt", "side 1\n")
	commitIn(t, r, "side 1")
	put(t, r, "b.txt", "side 2\n")
	commitIn(t, r, "side 2")
	r.Checkout("main")
	mustMerge(t, r, "side")
	put(t, r, "a.txt", "main 2\n")
	commitIn(t, r, "main 2")

	revs := []string{"HEAD", "HEAD~0", "HEAD^0", "HEAD~", "HEAD^", "HEAD~1", "HEAD~2", "HEAD~3",
		"HEAD^^", "HEAD~1^2", "HEAD^^2~1", "main~2", "side~1", "side^", "HEAD~1^1~1"}
	for _, rev := range revs {
		cmd := exec.Command(git, "--git-dir="+r.Dir, "rev-parse", "--verify", "-q", rev+"^{commit}")
		cmd.Env = append(os.Environ(), "GIT_CONFIG_NOSYSTEM=1")
		out, gerr := cmd.Output()
		want := strings.TrimSpace(string(out))
		got, err := r.Resolve(rev)
		if gerr != nil {
			t.Fatalf("git can't resolve %s", rev)
		}
		if err != nil || got != want {
			t.Errorf("%s: ours %s (%v), git %s", rev, got, err, want)
		}
	}
	for _, bad := range []string{"HEAD~99", "HEAD^3", "~1", "HEAD~x", "HEAD^2^2"} {
		if id, err := r.Resolve(bad); err == nil {
			t.Errorf("%s resolved to %s, want error", bad, id)
		}
	}
}
