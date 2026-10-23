// Package cmd implements the kk-branches command.
package cmd

import (
	"errors"
	"fmt"
	"os"

	"github.com/spf13/cobra"
)

// Execute runs the root command and returns a process exit code.
func Execute() int {
	if err := NewRoot().Execute(); err != nil {
		fmt.Fprintln(os.Stderr, "kk-branches:", err)
		return 1
	}
	return 0
}

// NewRoot builds the kk-branches cobra command tree.
func NewRoot() *cobra.Command {
	return &cobra.Command{
		Use:           "branches",
		Short:         "Interactive manager for local branches, their PRs, and worktrees",
		SilenceUsage:  true,
		SilenceErrors: true,
		RunE: func(*cobra.Command, []string) error {
			return errors.New("not implemented")
		},
	}
}
