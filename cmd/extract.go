package cmd

import (
	"fmt"
	"os"
	"path/filepath"

	"agetools/pkg/alf"
	"github.com/spf13/cobra"
)

var (
	extractFilter  string
	extractOutput  string
	extractVerbose bool
)

var extractCmd = &cobra.Command{
	Use:   "extract <archive>",
	Short: "Extract files from ALF archives",
	Long: `Extract files from Eushully AGE engine archives.

Supported formats:
  S4 (older games):
    - SYS4INI.BIN (S4IC): Main game archive index
    - APPENDxx.AAI (S4AC): Append archive index

  S5 (newer games):
    - SYS5INI.BIN (S5IN/S5IC): Main game archive index
    - APPENDxx.AAI (S5AC): Append archive index

The archive index file references one or more .alf files that contain
the actual file data. These .alf files must be in the same directory.

Examples:
  # Extract all files from SYS5INI.BIN
  agetools extract SYS5INI.BIN

  # Extract only .bin script files
  agetools extract SYS5INI.BIN -f .bin

  # Extract to a custom output directory
  agetools extract SYS5INI.BIN -o extracted/`,
	Args: cobra.ExactArgs(1),
	RunE: runExtract,
}

func init() {
	rootCmd.AddCommand(extractCmd)

	extractCmd.Flags().StringVarP(&extractFilter, "filter", "f", "",
		"filter extracted files (case-insensitive substring match)")
	extractCmd.Flags().StringVarP(&extractOutput, "output", "o", "data",
		"output directory for extracted files")
	extractCmd.Flags().BoolVarP(&extractVerbose, "verbose", "v", false,
		"print verbose progress information")
}

func runExtract(cmd *cobra.Command, args []string) error {
	archivePath := args[0]

	// Resolve to absolute path
	absPath, err := filepath.Abs(archivePath)
	if err != nil {
		return fmt.Errorf("failed to resolve path: %w", err)
	}

	// Check file exists
	if _, err := os.Stat(absPath); os.IsNotExist(err) {
		return fmt.Errorf("archive not found: %s", archivePath)
	}

	opts := alf.ExtractOptions{
		Filter:    extractFilter,
		OutputDir: extractOutput,
		Verbose:   extractVerbose,
	}

	extractor, err := alf.NewExtractor(absPath, opts)
	if err != nil {
		return fmt.Errorf("failed to create extractor: %w", err)
	}
	defer extractor.Close()

	if err := extractor.Open(absPath); err != nil {
		return fmt.Errorf("failed to open archive: %w", err)
	}

	archive := extractor.GetArchive()
	fmt.Printf("Extracting: %s\n", archive.Header.Title)
	fmt.Printf("Format: %s\n", archive.Header.Signature)
	fmt.Printf("Archives: %d\n", len(archive.Sources))
	fmt.Printf("Files: %d\n", len(archive.Entries))

	if extractFilter != "" {
		fmt.Printf("Filter: %s\n", extractFilter)
	}
	fmt.Println()

	if err := extractor.Extract(); err != nil {
		return fmt.Errorf("extraction failed: %w", err)
	}

	fmt.Println("Extraction complete!")
	return nil
}
