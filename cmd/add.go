package cmd

import (
	"errors"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/Kirch-log/kirch/internal/kicad"
	"github.com/spf13/cobra"
)

func init() {
	addCmd := &cobra.Command{
		Use:   "add <path>...",
		Short: "Stage files for the next commit",
		Long: `Stage files and folders for the next commit (folders are added recursively).
Circuit files (netlist.json, parts.json, layout.json, capture.json) are checked
and rewritten into Kirch's standard layout first. If any of them is invalid,
nothing is staged.

If netlist.json came from a KiCad schematic (kirch import kicad) and the
schematic changed, it is re-imported first and netlist.json / parts.json are
staged too.`,
		Args: cobra.MinimumNArgs(1),
		RunE: func(c *cobra.Command, args []string) error {
			r, err := currentRepo()
			if err != nil {
				return err
			}
			out := c.OutOrStdout()
			sync, err := r.SyncKiCad()
			switch {
			case errors.Is(err, kicad.ErrNoKiCad):
				fmt.Fprintf(c.ErrOrStderr(), "warning: %s may have changed but KiCad isn't installed here, so netlist.json wasn't updated\n", r.KiCadSchematic())
			case err != nil:
				return fmt.Errorf("re-importing the KiCad design: %w", err)
			case sync != nil && len(sync.Written) > 0:
				fmt.Fprintf(out, "re-imported %s → %s\n", sync.Schematic, strings.Join(sync.Written, ", "))
				for _, f := range sync.Written {
					args = append(args, filepath.Join(r.Root, f))
				}
			}
			res, err := r.Add(args)
			if err != nil {
				return err
			}
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
