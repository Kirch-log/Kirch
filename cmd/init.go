package cmd

import (
	"fmt"

	"github.com/Kirch-log/kirch/internal/repo"
	"github.com/spf13/cobra"
)

func init() {
	rootCmd.AddCommand(&cobra.Command{
		Use:   "init [folder]",
		Short: "Create an empty kirch repository",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(c *cobra.Command, args []string) error {
			folder := "."
			if len(args) == 1 {
				folder = args[0]
			}
			r, err := repo.Init(folder)
			if err != nil {
				return err
			}
			fmt.Fprintf(c.OutOrStdout(), "Initialized empty kirch repository in %s\n", r.Dir)
			return nil
		},
	})
}
