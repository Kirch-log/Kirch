package cmd

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/Kirch-log/kirch/internal/objects"
	"github.com/Kirch-log/kirch/internal/repo"
	"github.com/spf13/pflag"
)

// runKirch runs the CLI with args and returns what it printed.
func runKirch(t *testing.T, args ...string) string {
	t.Helper()
	var out bytes.Buffer
	rootCmd.SetOut(&out)
	rootCmd.SetErr(&out)
	rootCmd.SetArgs(args)
	defer resetFlags(t, args)
	if err := rootCmd.Execute(); err != nil {
		t.Fatalf("kirch %v: %v\n%s", args, err, out.String())
	}
	return out.String()
}

// resetFlags puts the command's flags back to their defaults; cobra keeps
// them between runs in the same process.
func resetFlags(t *testing.T, args []string) {
	rootCmd.SetArgs(nil)
	c, _, err := rootCmd.Find(args)
	if err != nil {
		return
	}
	c.Flags().VisitAll(func(f *pflag.Flag) {
		if err := f.Value.Set(f.DefValue); err != nil {
			t.Fatal(err)
		}
		f.Changed = false
	})
}

// Oracle: kirch log prints exactly what git log prints for the same .kirch
// folder (medium and --oneline formats, merges, several branches).
func TestLogOutputMatchesGit(t *testing.T) {
	git, err := exec.LookPath("git")
	if err != nil {
		t.Skip("git not installed")
	}
	root := t.TempDir()
	r, err := repo.Init(root)
	if err != nil {
		t.Fatal(err)
	}
	t.Chdir(root)

	tree, err := r.Objects.Write(objects.Tree, nil)
	if err != nil {
		t.Fatal(err)
	}
	zone := time.FixedZone("", 5*3600+30*60)
	commit := func(msg string, secs int64, parents ...string) string {
		sig := objects.Signature{Name: "Test Author", Email: "test@example.com", When: time.Unix(secs, 0).In(zone)}
		data, err := objects.EncodeCommit(objects.CommitData{
			Tree: tree, Parents: parents, Author: sig, Committer: sig, Message: msg,
		})
		if err != nil {
			t.Fatal(err)
		}
		id, err := r.Objects.Write(objects.Commit, data)
		if err != nil {
			t.Fatal(err)
		}
		return id
	}
	base := int64(1794621600)
	c1 := commit("First circuit\n", base)
	c2 := commit("Move servo to GPIO10\nwired on the left rail\n\nOld pin was flaky.  \n\n  Checked with the probe.\n", base+60, c1)
	c3 := commit("Add ultrasonic sensor\n", base+120, c1)
	c4 := commit("Merge sensor branch\n", base+180, c2, c3)
	for name, id := range map[string]string{"main": c4, "sensor": c3, "zz-old": c1, "a-old": c1} {
		if err := r.Refs().UpdateBranch(name, id, ""); err != nil {
			t.Fatal(err)
		}
	}

	globalCfg := filepath.Join(t.TempDir(), "gitconfig")
	if err := os.WriteFile(globalCfg, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	gitLog := func(args ...string) string {
		cmd := exec.Command(git, append([]string{"--git-dir=.kirch", "log", "--decorate=short", "--no-color"}, args...)...)
		cmd.Env = append(os.Environ(), "GIT_CONFIG_NOSYSTEM=1", "GIT_CONFIG_GLOBAL="+globalCfg)
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("git log %v: %v\n%s", args, err, out)
		}
		return string(out)
	}

	for _, args := range [][]string{
		{},
		{"--oneline"},
		{"-n", "2"},
		{"sensor"},
		{c2[:7]},
	} {
		got := runKirch(t, append([]string{"log"}, args...)...)
		if want := gitLog(args...); got != want {
			t.Errorf("kirch log %v:\n%s\ngit log:\n%s", args, got, want)
		}
	}
}
