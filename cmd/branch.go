package cmd

import (
	"fmt"
	"sort"

	"github.com/spf13/cobra"
)

func init() {
	var del, forceDel bool
	branchCmd := &cobra.Command{
		Use:   "branch [<name> [start]] | -d <name>",
		Short: "List, create or delete branches",
		Long: `kirch branch                 list branches (* = current)
kirch branch <name> [start]  create a branch at start (default: current commit)
kirch branch -d <name>       delete a branch that is merged into the current one
kirch branch -D <name>       delete a branch even if its commits would be lost`,
		Args: cobra.MaximumNArgs(2),
		RunE: func(c *cobra.Command, args []string) error {
			r, err := currentRepo()
			if err != nil {
				return err
			}
			out := c.OutOrStdout()
			switch {
			case del || forceDel:
				if len(args) != 1 {
					return fmt.Errorf("give one branch to delete")
				}
				id, err := r.DeleteBranch(args[0], forceDel)
				if err != nil {
					return err
				}
				fmt.Fprintf(out, "Deleted branch %s (was %s).\n", args[0], id[:7])
			case len(args) > 0:
				start := ""
				if len(args) == 2 {
					start = args[1]
				}
				_, err := r.CreateBranch(args[0], start)
				return err
			default:
				current, _, err := r.Refs().Head()
				if err != nil {
					return err
				}
				branches, err := r.Refs().Branches()
				if err != nil {
					return err
				}
				names := make([]string, 0, len(branches))
				for name := range branches {
					names = append(names, name)
				}
				sort.Strings(names)
				for _, name := range names {
					mark := " "
					if name == current {
						mark = "*"
					}
					fmt.Fprintf(out, "%s %s\n", mark, name)
				}
			}
			return nil
		},
	}
	branchCmd.Flags().BoolVarP(&del, "delete", "d", false, "delete a merged branch")
	branchCmd.Flags().BoolVarP(&forceDel, "force-delete", "D", false, "delete a branch even if not merged")
	branchCmd.MarkFlagsMutuallyExclusive("delete", "force-delete")

	var newBranch bool
	checkoutCmd := &cobra.Command{
		Use:   "checkout <branch> | -b <new branch> [start]",
		Short: "Switch to another branch (files are updated to match it)",
		Args:  cobra.RangeArgs(1, 2),
		RunE: func(c *cobra.Command, args []string) error {
			r, err := currentRepo()
			if err != nil {
				return err
			}
			out := c.OutOrStdout()
			name := args[0]
			if newBranch {
				start := ""
				if len(args) == 2 {
					start = args[1]
				}
				if _, err := r.CreateBranch(name, start); err != nil {
					return err
				}
				if err := r.Checkout(name); err != nil {
					// Undo the branch we just made; nothing else changed.
					_, _ = r.DeleteBranch(name, true)
					return err
				}
				fmt.Fprintf(out, "Switched to a new branch '%s'\n", name)
				return nil
			}
			if len(args) == 2 {
				return fmt.Errorf("a start point only goes with -b")
			}
			current, _, err := r.Refs().Head()
			if err != nil {
				return err
			}
			if name == current {
				fmt.Fprintf(out, "Already on '%s'\n", name)
				return nil
			}
			if id, _ := r.Refs().Branch(name); id == "" {
				if _, err := r.Resolve(name); err == nil {
					return fmt.Errorf("%q is a commit, not a branch; to work from it, make a branch: kirch checkout -b <name> %s", name, name)
				}
				return fmt.Errorf("branch %q not found", name)
			}
			if err := r.Checkout(name); err != nil {
				return err
			}
			fmt.Fprintf(out, "Switched to branch '%s'\n", name)
			return nil
		},
	}
	checkoutCmd.Flags().BoolVarP(&newBranch, "branch", "b", false, "create the branch, then switch to it")

	rootCmd.AddCommand(branchCmd, checkoutCmd)
}
