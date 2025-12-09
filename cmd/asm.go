package cmd

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"agetools/pkg/bin"

	"github.com/spf13/cobra"
)

var asmCmd = &cobra.Command{
	Use:   "asm <file.txt> [output.bin]",
	Short: "Assemble BIN script files",
	Long: `Assemble human-readable assembly text back to Eushully AGE engine BIN files.

Examples:
  agetools asm BUNKI.txt                       # Output to BUNKI.BIN
  agetools asm BUNKI.txt output.bin            # Output to output.bin
  agetools asm --dir ./scripts                 # Assemble all .txt files in directory`,
	Args: cobra.MinimumNArgs(0),
	RunE: runAsm,
}

var (
	asmDir string
)

func init() {
	rootCmd.AddCommand(asmCmd)
	asmCmd.Flags().StringVarP(&asmDir, "dir", "d", "", "Process all .txt files in directory")
}

func runAsm(cmd *cobra.Command, args []string) error {
	// Directory mode
	if asmDir != "" {
		return asmDirectory(asmDir)
	}

	// Single file mode
	if len(args) < 1 {
		return fmt.Errorf("either --dir or a file path is required")
	}

	inputPath := args[0]
	outputPath := ""
	if len(args) >= 2 {
		outputPath = args[1]
	} else {
		// Default output path
		ext := filepath.Ext(inputPath)
		outputPath = strings.TrimSuffix(inputPath, ext) + ".BIN"
	}

	return asmFile(inputPath, outputPath)
}

func asmFile(inputPath, outputPath string) error {
	// Read input file
	text, err := os.ReadFile(inputPath)
	if err != nil {
		return fmt.Errorf("failed to read %s: %w", inputPath, err)
	}

	// Assemble
	result, err := bin.Assemble(string(text), bin.FormatSYS5)
	if err != nil {
		return fmt.Errorf("failed to assemble %s: %w", inputPath, err)
	}

	// Write output
	if err := os.WriteFile(outputPath, result.Data, 0644); err != nil {
		return fmt.Errorf("failed to write %s: %w", outputPath, err)
	}

	fmt.Printf("Assembled %s -> %s (%d bytes)\n",
		filepath.Base(inputPath), filepath.Base(outputPath), len(result.Data))

	return nil
}

func asmDirectory(dir string) error {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return fmt.Errorf("failed to read directory %s: %w", dir, err)
	}

	processed := 0
	errors := 0

	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}

		name := entry.Name()
		if !strings.HasSuffix(strings.ToLower(name), ".txt") {
			continue
		}

		inputPath := filepath.Join(dir, name)
		outputPath := filepath.Join(dir, strings.TrimSuffix(name, filepath.Ext(name))+".BIN")

		if err := asmFile(inputPath, outputPath); err != nil {
			fmt.Fprintf(os.Stderr, "Error processing %s: %v\n", name, err)
			errors++
		} else {
			processed++
		}
	}

	fmt.Printf("\nProcessed %d files, %d errors\n", processed, errors)
	return nil
}
