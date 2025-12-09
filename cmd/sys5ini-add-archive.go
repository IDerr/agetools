package cmd

import (
	"fmt"
	"os"
	"path/filepath"

	"agetools/pkg/alf"
	"github.com/spf13/cobra"
)

var (
	addArchiveOutput  string
	addArchiveVerbose bool
)

var sys5iniAddArchiveCmd = &cobra.Command{
	Use:   "sys5ini-add-archive <sys5ini.bin> <archive-name> <input-dir>",
	Short: "Add a new archive to SYS5INI.BIN",
	Long: `Add a new DATA*.ALF archive entry to SYS5INI.BIN.

This command:
  1. Reads the existing SYS5INI.BIN
  2. Adds a new archive entry (e.g., DATA9.ALF)
  3. Creates the new DATA*.ALF file with files from input directory
  4. Writes modified SYS5INI.BIN to output path

Examples:
  # Add DATA9.ALF with files from data9/DATA9/
  agetools sys5ini-add-archive SYS5INI.BIN DATA9.ALF data9/DATA9/ -o SYS5INI_new.BIN

  # Add with verbose output
  agetools sys5ini-add-archive SYS5INI.BIN DATA9.ALF data9/DATA9/ -o SYS5INI_new.BIN -v`,
	Args: cobra.ExactArgs(3),
	RunE: runSys5iniAddArchive,
}

func init() {
	rootCmd.AddCommand(sys5iniAddArchiveCmd)

	sys5iniAddArchiveCmd.Flags().StringVarP(&addArchiveOutput, "output", "o", "SYS5INI_modified.BIN",
		"output path for modified SYS5INI.BIN")
	sys5iniAddArchiveCmd.Flags().BoolVarP(&addArchiveVerbose, "verbose", "v", false,
		"print verbose progress information")
}

func runSys5iniAddArchive(cmd *cobra.Command, args []string) error {
	sys5iniPath := args[0]
	archiveName := args[1]
	inputDir := args[2]

	// Resolve paths
	absSys5ini, err := filepath.Abs(sys5iniPath)
	if err != nil {
		return fmt.Errorf("failed to resolve sys5ini path: %w", err)
	}

	absInput, err := filepath.Abs(inputDir)
	if err != nil {
		return fmt.Errorf("failed to resolve input dir: %w", err)
	}

	absOutput, err := filepath.Abs(addArchiveOutput)
	if err != nil {
		return fmt.Errorf("failed to resolve output path: %w", err)
	}

	// Check sys5ini exists
	if _, err := os.Stat(absSys5ini); os.IsNotExist(err) {
		return fmt.Errorf("SYS5INI.BIN not found: %s", sys5iniPath)
	}

	// Check input directory exists
	if _, err := os.Stat(absInput); os.IsNotExist(err) {
		return fmt.Errorf("input directory not found: %s", inputDir)
	}

	opts := alf.AddArchiveOptions{
		ArchiveName: archiveName,
		InputDir:    absInput,
		OutputPath:  absOutput,
		Verbose:     addArchiveVerbose,
	}

	if err := alf.AddArchive(absSys5ini, opts); err != nil {
		return fmt.Errorf("failed to add archive: %w", err)
	}

	fmt.Printf("\nSuccess! Modified SYS5INI.BIN written to: %s\n", absOutput)
	fmt.Printf("New archive created: %s\n", filepath.Join(filepath.Dir(absOutput), archiveName))

	return nil
}
