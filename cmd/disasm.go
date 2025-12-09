package cmd

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"agetools/pkg/bin"

	"github.com/spf13/cobra"
)

var disasmCmd = &cobra.Command{
	Use:   "disasm <file.bin> [output.txt]",
	Short: "Disassemble BIN script files",
	Long: `Disassemble Eushully AGE engine BIN script files to human-readable assembly.

Examples:
  agetools disasm BUNKI.BIN                    # Output to BUNKI.txt
  agetools disasm BUNKI.BIN output.txt         # Output to output.txt
  agetools disasm --dir ./scripts              # Disassemble all .bin files in directory
  agetools disasm BUNKI.BIN --verify           # Verify round-trip`,
	Args: cobra.MinimumNArgs(0),
	RunE: runDisasm,
}

var (
	disasmDir    string
	disasmVerify bool
)

func init() {
	rootCmd.AddCommand(disasmCmd)
	disasmCmd.Flags().StringVarP(&disasmDir, "dir", "d", "", "Process all .bin files in directory")
	disasmCmd.Flags().BoolVarP(&disasmVerify, "verify", "v", false, "Verify round-trip (disasm -> asm -> compare)")
}

func runDisasm(cmd *cobra.Command, args []string) error {
	// Directory mode
	if disasmDir != "" {
		return disasmDirectory(disasmDir)
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
		outputPath = strings.TrimSuffix(inputPath, ext) + ".txt"
	}

	return disasmFile(inputPath, outputPath)
}

func disasmFile(inputPath, outputPath string) error {
	// Read input file
	data, err := os.ReadFile(inputPath)
	if err != nil {
		return fmt.Errorf("failed to read %s: %w", inputPath, err)
	}

	// Verify round-trip if requested
	if disasmVerify {
		matches, err := bin.VerifyRoundTrip(data)
		if err != nil {
			fmt.Printf("Verify failed for %s: %v\n", inputPath, err)
		} else if matches {
			fmt.Printf("Verify OK: %s\n", inputPath)
		} else {
			fmt.Printf("Verify MISMATCH: %s\n", inputPath)
		}
	}

	// Disassemble
	script, err := bin.Disassemble(data)
	if err != nil {
		return fmt.Errorf("failed to disassemble %s: %w", inputPath, err)
	}

	// Convert to text
	text := script.ToText()

	// Write output
	if err := os.WriteFile(outputPath, []byte(text), 0644); err != nil {
		return fmt.Errorf("failed to write %s: %w", outputPath, err)
	}

	fmt.Printf("Disassembled %s -> %s (%d instructions)\n",
		filepath.Base(inputPath), filepath.Base(outputPath), len(script.Instructions))

	return nil
}

func disasmDirectory(dir string) error {
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
		if !strings.HasSuffix(strings.ToLower(name), ".bin") {
			continue
		}

		inputPath := filepath.Join(dir, name)
		outputPath := filepath.Join(dir, strings.TrimSuffix(name, filepath.Ext(name))+".txt")

		if err := disasmFile(inputPath, outputPath); err != nil {
			fmt.Fprintf(os.Stderr, "Error processing %s: %v\n", name, err)
			errors++
		} else {
			processed++
		}
	}

	fmt.Printf("\nProcessed %d files, %d errors\n", processed, errors)
	return nil
}
