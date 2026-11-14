package cmd

import (
	"fmt"
	"io"
	"os"
	"path"

	"github.com/Kirch-log/kirch/internal/diff"
	"github.com/Kirch-log/kirch/internal/repo"
	"github.com/spf13/cobra"
)

func init() {
	var staged, text bool
	diffCmd := &cobra.Command{
		Use:   "diff [--staged] [<commit> [<commit>]] [--] [<path>...]",
		Short: "Show changes: files vs staged, staged vs last commit, or between commits",
		Long: `kirch diff                  changes not staged yet (files vs index)
kirch diff --staged         what the next commit will change (last commit vs index)
kirch diff <commit>         files vs that commit
kirch diff <a> <b>          from commit a to commit b
Add paths (after -- if they could be mistaken for a branch) to show only those files.

netlist.json changes are shown as wiring changes (which pins moved where).
Use --text for a plain git-style patch of every file.`,
		RunE: func(c *cobra.Command, args []string) error {
			r, err := currentRepo()
			if err != nil {
				return err
			}
			revs, paths, err := splitRevsAndPaths(r, args, c.ArgsLenAtDash())
			if err != nil {
				return err
			}
			var files []diff.File
			switch {
			case len(revs) > 2:
				return fmt.Errorf("give at most two commits")
			case staged && len(revs) > 0:
				return fmt.Errorf("--staged compares the last commit with the index; it takes no commits")
			case staged:
				files, err = r.DiffStaged()
			case len(revs) == 2:
				files, err = r.DiffCommits(revs[0], revs[1])
			case len(revs) == 1:
				files, err = r.DiffWorktree(revs[0])
			default:
				files, err = r.DiffWorktree("")
			}
			if err != nil {
				return err
			}
			files = repo.FilterPaths(files, paths)
			out := c.OutOrStdout()
			for _, f := range files {
				if text || !writeWiring(out, f) {
					diff.WritePatch(out, f)
				}
			}
			return nil
		},
	}
	diffCmd.Flags().BoolVar(&staged, "staged", false, "compare the last commit with the index")
	diffCmd.Flags().BoolVar(&staged, "cached", false, "same as --staged")
	diffCmd.Flags().BoolVar(&text, "text", false, "plain git-style patch, also for netlist.json")
	rootCmd.AddCommand(diffCmd)
}

// splitRevsAndPaths separates commits from paths. Everything after "--"
// is a path; before it, an argument is a commit if it names one, else a
// path if it exists on disk.
func splitRevsAndPaths(r *repo.Repo, args []string, dash int) (revs, paths []string, err error) {
	if dash < 0 {
		dash = len(args)
	}
	for i, a := range args {
		if i < dash {
			if _, err := r.Resolve(a); err == nil {
				if len(paths) > 0 {
					return nil, nil, fmt.Errorf("%q: put commits before paths", a)
				}
				revs = append(revs, a)
				continue
			}
			if _, err := os.Stat(a); err != nil {
				return nil, nil, fmt.Errorf("%q is not a branch, commit or file (use -- before deleted paths)", a)
			}
		}
		rel, err := r.RelPath(a)
		if err != nil {
			return nil, nil, err
		}
		paths = append(paths, rel)
	}
	return revs, paths, nil
}

// writeWiring shows a netlist.json change as wiring changes. It returns
// false (and writes nothing) when that isn't possible or says nothing, so
// the caller falls back to a text patch.
func writeWiring(w io.Writer, f diff.File) bool {
	if path.Base(f.Path) != "netlist.json" || f.OldMode != f.NewMode && f.OldID != "" && f.NewID != "" {
		return false
	}
	changes, err := diff.Wiring(f.Old, f.New)
	if err != nil || len(changes) == 0 {
		return false
	}
	diff.WriteHeader(w, f)
	fmt.Fprintln(w, "wiring:")
	for _, ch := range changes {
		fmt.Fprintf(w, "    %s\n", ch)
	}
	return true
}
