// Package cmd holds the Cobra commands. They only parse arguments and print;
// all real work is in internal/ so the agent can call the same functions.
package cmd

import (
	"errors"
	"fmt"

	"github.com/Kirch-log/kirch/internal/repo"
	"github.com/spf13/cobra"
)

// version is set by release builds:
// go build -ldflags "-X github.com/Kirch-log/kirch/cmd.version=v0.1.0"
var version = "dev"

var rootCmd = &cobra.Command{
	Use:           "kirch",
	Short:         "Version control for hardware circuits",
	Version:       version,
	SilenceUsage:  true,
	SilenceErrors: true, // Execute prints them
}

// errSilentFail makes kirch exit with status 1 after a command already
// explained what went wrong (e.g. a merge with conflicts).
var errSilentFail = errors.New("failed")

// Execute runs the CLI.
func Execute() error {
	err := rootCmd.Execute()
	if err != nil && !errors.Is(err, errSilentFail) {
		fmt.Fprintln(rootCmd.ErrOrStderr(), "Error:", err)
	}
	return err
}

// currentRepo opens the repository containing the current folder.
func currentRepo() (*repo.Repo, error) {
	return repo.Find(".")
}
