package repo

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

const ignoreRules = `# build output
*.exe
!keep.exe
build/
/notes.txt
docs/*.pdf
*.log
`

var ignoreFiles = []string{
	"kirch.exe", "keep.exe", "sub/tool.exe", "build/out.bin", "sub/build/x.o",
	"notes.txt", "sub/notes.txt", "docs/a.pdf", "docs/deep/b.pdf", "a.log",
	"netlist.json", "build.txt", "logs/today.log",
}

// Oracle: with the same patterns, kirch status lists the same untracked
// files as git status (git reads them from .git/info/exclude).
func TestIgnoreMatchesGit(t *testing.T) {
	git, err := exec.LookPath("git")
	if err != nil {
		t.Skip("git not installed")
	}
	r := newRepo(t, nil)
	gitRoot := t.TempDir()
	for _, root := range []string{r.Root, gitRoot} {
		for _, f := range ignoreFiles {
			content := "x\n"
			if f == "netlist.json" {
				content = netlist
			}
			writeFile(t, filepath.Join(root, filepath.FromSlash(f)), content)
		}
		writeFile(t, filepath.Join(root, IgnoreFile), ignoreRules)
	}
	run := func(args ...string) string {
		cmd := exec.Command(git, args...)
		cmd.Dir = gitRoot
		cmd.Env = append(os.Environ(), "GIT_CONFIG_NOSYSTEM=1", "GIT_CONFIG_GLOBAL="+os.DevNull)
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
		return string(out)
	}
	run("init", "-q")
	writeFile(t, filepath.Join(gitRoot, ".git", "info", "exclude"), ignoreRules)
	want := run("status", "--porcelain")

	st, err := r.Status()
	if err != nil {
		t.Fatal(err)
	}
	var got strings.Builder
	for _, p := range st.Untracked {
		got.WriteString("?? " + p + "\n")
	}
	if got.String() != want {
		t.Fatalf("kirch untracked:\n%s\ngit:\n%s", got.String(), want)
	}
}

func TestIgnoreAddAndTracked(t *testing.T) {
	r := newRepo(t, map[string]string{
		IgnoreFile: "*.exe\nbuild/\n", "netlist.json": netlist, "kirch.exe": "bin", "build/out.bin": "o",
	})
	addAll(t, r)
	if got := strings.Join(staged(t, r), ","); got != ".kirchignore,netlist.json" {
		t.Fatalf("add . staged %s", got)
	}
	// Naming an ignored file adds it anyway; after that it's tracked and
	// its changes show up like any other file.
	if _, err := r.Add([]string{"kirch.exe"}); err != nil {
		t.Fatal(err)
	}
	commitIn(t, r, "with exe")
	writeFile(t, filepath.Join(r.Root, "kirch.exe"), "new bin")
	wantStatus(t, r, " M kirch.exe")
	addAll(t, r)
	wantStatus(t, r, "M  kirch.exe")
}
