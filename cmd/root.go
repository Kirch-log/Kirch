// Package cmd holds the Cobra commands. They only parse arguments and print;
// all real work is in internal/ so the agent can call the same functions.
package cmd

import (
	"github.com/Kirch-log/kirch/internal/repo"
	"github.com/spf13/cobra"
)

var rootCmd = &cobra.Command{
	Use:          "kirch",
	Short:        "Version control for hardware circuits",
	SilenceUsage: true,
}

// Execute runs the CLI.
func Execute() error {
	return rootCmd.Execute()
}

// currentRepo opens the repository containing the current folder.
func currentRepo() (*repo.Repo, error) {
	return repo.Find(".")
}
