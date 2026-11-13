package cmd

import (
	"fmt"

	"github.com/spf13/cobra"
)

func init() {
	addCmd := &cobra.Command{
		Use:   "add <path>...",
		Short: "Stage files for the next commit",
		Long: `Stage files and folders for the next commit (folders are added recursively).
Circuit files (netlist.json, parts.json, layout.json, capture.json) are checked
and rewritten into Kirch's standard layout first. If any of them is invalid,
nothing is staged.`,
		Args: cobra.MinimumNArgs(1),
		RunE: func(c *cobra.Command, args []string) error {
			r, err := currentRepo()
			if err != nil {
				return err
			}
			res, err := r.Add(args)
			if err != nil {
				return err
			}
			out := c.OutOrStdout()
			for _, p := range res.Rewritten {
				fmt.Fprintf(out, "formatted %s\n", p)
			}
			for _, p := range res.Staged {
				fmt.Fprintf(out, "staged    %s\n", p)
			}
			for _, p := range res.Removed {
				fmt.Fprintf(out, "removed   %s\n", p)
			}
			return nil
		},
	}

	lsFiles := &cobra.Command{
		Use:   "ls-files",
		Short: "List staged files as \"mode id stage<TAB>path\" (same as git ls-files -s)",
		Args:  cobra.NoArgs,
		RunE: func(c *cobra.Command, args []string) error {
			r, err := currentRepo()
			if err != nil {
				return err
			}
			idx, err := r.LoadIndex()
			if err != nil {
				return err
			}
			for _, p := range idx.Paths() {
				e := idx.Entries[p]
				fmt.Fprintf(c.OutOrStdout(), "%s %s 0\t%s\n", e.Mode, e.Hash, p)
			}
			return nil
		},
	}

	rootCmd.AddCommand(addCmd, lsFiles)
}
