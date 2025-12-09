package cmd

import (
	"fmt"
	"os"
	"path/filepath"

	"agetools/pkg/alf"
	"github.com/spf13/cobra"
)

var (
	packOutput  string
	packVerbose bool
)

var packCmd = &cobra.Command{
	Use:   "pack <original_archive> <input_dir>",
	Short: "Pack files into ALF archives",
	Long: `Pack modified files back into Eushully AGE engine archives.

This command takes an original archive index file (SYS5INI.BIN) and a directory
containing modified files, and creates new archive files with the modifications.

The input directory structure should match the extraction output:
  input_dir/
    DATA1/
      file1.bin
      file2.bin
    DATA2/
      ...

Files that exist in the input directory will be used; missing files will be
copied from the original archives.

Examples:
  # Repack with modifications from data/ directory
  agetools pack SYS5INI.BIN data/ -o output/

  # Repack with verbose output
  agetools pack SYS5INI.BIN modified/ -o repacked/ -v`,
	Args: cobra.ExactArgs(2),
	RunE: runPack,
}

func init() {
	rootCmd.AddCommand(packCmd)

	packCmd.Flags().StringVarP(&packOutput, "output", "o", "repacked",
		"output directory for repacked archives")
	packCmd.Flags().BoolVarP(&packVerbose, "verbose", "v", false,
		"print verbose progress information")
}

func runPack(cmd *cobra.Command, args []string) error {
	originalPath := args[0]
	inputDir := args[1]

	// Resolve paths
	absOriginal, err := filepath.Abs(originalPath)
	if err != nil {
		return fmt.Errorf("failed to resolve original path: %w", err)
	}

	absInput, err := filepath.Abs(inputDir)
	if err != nil {
		return fmt.Errorf("failed to resolve input path: %w", err)
	}

	// Check original exists
	if _, err := os.Stat(absOriginal); os.IsNotExist(err) {
		return fmt.Errorf("original archive not found: %s", originalPath)
	}

	// Check input directory exists
	if info, err := os.Stat(absInput); os.IsNotExist(err) {
		return fmt.Errorf("input directory not found: %s", inputDir)
	} else if !info.IsDir() {
		return fmt.Errorf("input path is not a directory: %s", inputDir)
	}

	// Create output directory
	absOutput, err := filepath.Abs(packOutput)
	if err != nil {
		return fmt.Errorf("failed to resolve output path: %w", err)
	}

	if err := os.MkdirAll(absOutput, 0755); err != nil {
		return fmt.Errorf("failed to create output directory: %w", err)
	}

	opts := alf.PackOptions{
		OutputDir:   absOutput,
		Verbose:     packVerbose,
		OriginalBIN: absOriginal,
	}

	packer, err := alf.NewPacker(absInput, opts)
	if err != nil {
		return fmt.Errorf("failed to create packer: %w", err)
	}
	defer packer.Close()

	fmt.Printf("Loading original archive: %s\n", originalPath)
	if err := packer.LoadOriginal(absOriginal); err != nil {
		return fmt.Errorf("failed to load original archive: %w", err)
	}

	fmt.Printf("Input directory: %s\n", inputDir)
	fmt.Printf("Output directory: %s\n", packOutput)
	fmt.Println()

	if err := packer.Pack(); err != nil {
		return fmt.Errorf("packing failed: %w", err)
	}

	fmt.Println("Packing complete!")
	return nil
}
