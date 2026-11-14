package cmd

import (
	"fmt"
	"io"

	"github.com/Kirch-log/kirch/internal/repo"
	"github.com/spf13/cobra"
)

func init() {
	var abort bool
	mergeCmd := &cobra.Command{
		Use:   "merge <branch> | --abort",
		Short: "Join another branch into the current one",
		Long: `kirch merge <branch>   join branch into the current branch
kirch merge --abort    give up an unfinished merge (files go back to the last commit)

Text files merge line by line. netlist.json merges pin by pin: a conflict only
happens when both branches connected the same pin differently. On a conflict,
fix the files (in netlist.json, the pins listed under "conflicts"), then
kirch add them and kirch commit.`,
		RunE: func(c *cobra.Command, args []string) error {
			r, err := currentRepo()
			if err != nil {
				return err
			}
			out := c.OutOrStdout()
			if abort {
				if len(args) > 0 {
					return fmt.Errorf("--abort takes no branch")
				}
				return r.MergeAbort()
			}
			if len(args) != 1 {
				return fmt.Errorf("give one branch (or commit) to merge")
			}
			author, err := authorFor(r)
			if err != nil {
				return err
			}
			res, err := r.Merge(args[0], author)
			if err != nil {
				return err
			}
			switch {
			case res.UpToDate:
				fmt.Fprintln(out, "Already up to date.")
			case res.FastForward:
				from := "(no commits)"
				if res.From != "" {
					from = res.From[:7]
				}
				fmt.Fprintf(out, "Updating %s..%s\nFast-forward\n", from, res.To[:7])
			case len(res.Conflicts) > 0:
				printConflicts(out, res.Conflicts)
				fmt.Fprintln(out, "Automatic merge failed; fix conflicts, kirch add the files, then kirch commit.")
				return errSilentFail
			default:
				fmt.Fprintf(out, "Merge made: [%s %s] %s\n", res.Commit.Branch, res.Commit.ID[:7], repo.MergeMessage(args[0], res.Commit.Branch, isBranch(r, args[0])))
			}
			return nil
		},
	}
	mergeCmd.Flags().BoolVar(&abort, "abort", false, "give up an unfinished merge")
	rootCmd.AddCommand(mergeCmd)
}

func isBranch(r *repo.Repo, name string) bool {
	id, err := r.Refs().Branch(name)
	return err == nil && id != ""
}

func printConflicts(w io.Writer, conflicts []repo.Conflict) {
	for _, c := range conflicts {
		switch c.Kind {
		case "wiring":
			fmt.Fprintf(w, "CONFLICT (wiring): %s — both branches rewired the same nets differently:\n", c.Path)
			for _, d := range c.Details {
				fmt.Fprintf(w, "    %s\n", d)
			}
		case "modify/delete":
			fmt.Fprintf(w, "CONFLICT (modify/delete): %s deleted on one branch, changed on the other\n", c.Path)
		default:
			fmt.Fprintf(w, "CONFLICT (%s): Merge conflict in %s\n", c.Kind, c.Path)
		}
	}
}
