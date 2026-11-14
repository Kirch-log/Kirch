package cmd

import (
	"fmt"
	"io"

	"github.com/Kirch-log/kirch/internal/repo"
	"github.com/spf13/cobra"
)

func init() {
	var soft, hard bool
	resetCmd := &cobra.Command{
		Use:   "reset [--soft | --hard] [<commit>] [--] [<path>...]",
		Short: "Move the current branch back, or unstage files",
		Long: `kirch reset <path>...        unstage files (the files themselves are kept)
kirch reset <commit>         move the branch to commit; staged changes become unstaged
kirch reset --soft <commit>  move the branch only; everything since stays staged
kirch reset --hard <commit>  move the branch and make the files match the commit
                             (uncommitted changes are lost; untracked files are kept)
<commit> defaults to HEAD, so "kirch reset --hard" throws away uncommitted changes.`,
		RunE: func(c *cobra.Command, args []string) error {
			r, err := currentRepo()
			if err != nil {
				return err
			}
			revs, paths, err := splitRevsAndPaths(r, args, c.ArgsLenAtDash())
			if err != nil {
				return err
			}
			if len(revs) > 1 {
				return fmt.Errorf("give one commit")
			}
			rev := ""
			if len(revs) == 1 {
				rev = revs[0]
			}
			out := c.OutOrStdout()
			if len(paths) > 0 {
				if soft || hard {
					return fmt.Errorf("--soft and --hard move the whole branch; they don't take paths")
				}
				if _, err := r.ResetPaths(rev, paths); err != nil {
					return err
				}
				return printUnstaged(out, r)
			}
			mode := repo.ResetMixed
			switch {
			case soft:
				mode = repo.ResetSoft
			case hard:
				mode = repo.ResetHard
			}
			if rev == "" {
				rev = "HEAD"
			}
			if _, err := r.Reset(rev, mode); err != nil {
				return err
			}
			switch mode {
			case repo.ResetHard:
				id, err := r.Resolve("HEAD")
				if err != nil {
					return err
				}
				cm, err := r.ReadCommit(id)
				if err != nil {
					return err
				}
				fmt.Fprintf(out, "HEAD is now at %s %s\n", id[:7], firstLine(cm.Message))
			case repo.ResetMixed:
				return printUnstaged(out, r)
			}
			return nil
		},
	}
	resetCmd.Flags().BoolVar(&soft, "soft", false, "move the branch only")
	resetCmd.Flags().BoolVar(&hard, "hard", false, "also overwrite files (uncommitted changes are lost)")
	resetCmd.MarkFlagsMutuallyExclusive("soft", "hard")

	revertCmd := &cobra.Command{
		Use:   "revert <commit>",
		Short: "Make a new commit that undoes an earlier one (history is kept)",
		Args:  cobra.ExactArgs(1),
		RunE: func(c *cobra.Command, args []string) error {
			r, err := currentRepo()
			if err != nil {
				return err
			}
			author, err := authorFor(r)
			if err != nil {
				return err
			}
			res, err := r.Revert(args[0], author)
			if err != nil {
				return err
			}
			cm, err := r.ReadCommit(res.ID)
			if err != nil {
				return err
			}
			fmt.Fprintf(c.OutOrStdout(), "[%s %s] %s\n", res.Branch, res.ID[:7], firstLine(cm.Message))
			return nil
		},
	}

	rootCmd.AddCommand(resetCmd, revertCmd)
}

// printUnstaged lists files whose changes are not staged, like git reset.
func printUnstaged(w io.Writer, r *repo.Repo) error {
	st, err := r.Status()
	if err != nil {
		return err
	}
	header := false
	for _, f := range st.Files {
		if f.Unstaged == repo.Unchanged {
			continue
		}
		if !header {
			fmt.Fprintln(w, "Unstaged changes after reset:")
			header = true
		}
		fmt.Fprintf(w, "%c\t%s\n", f.Unstaged, f.Path)
	}
	return nil
}
