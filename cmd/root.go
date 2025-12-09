package cmd

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"
)

var rootCmd = &cobra.Command{
	Use:   "agetools",
	Short: "Tools for Eushully AGE engine games",
	Long: `agetools provides utilities for working with Eushully AGE engine game files.

Supported operations:
  - Extract files from ALF archives (SYS5INI.BIN, APPENDxx.AAI)
  - Disassemble BIN script files (coming soon)
  - Reassemble BIN script files (coming soon)`,
}

func Execute() {
	if err := rootCmd.Execute(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func init() {
	rootCmd.CompletionOptions.DisableDefaultCmd = true
}
