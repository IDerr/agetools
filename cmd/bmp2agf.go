package cmd

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/agetools/pkg/agf"
	"github.com/spf13/cobra"
)

var (
	bmp2agfOutput   string
	bmp2agfOriginal string
	bmp2agfVerbose  bool
)

var bmp2agfCmd = &cobra.Command{
	Use:   "bmp2agf <input.bmp> [output.agf]",
	Short: "Convert BMP image to AGF",
	Long: `Convert BMP image files back to Eushully AGF format.

Requires the original AGF file as reference to preserve format metadata.
The original AGF determines whether the output is 24-bit or 32-bit.

Examples:
  # Convert single file (auto-detect original AGF)
  agetools bmp2agf image.BMP

  # Convert with explicit original AGF
  agetools bmp2agf image.BMP -r original/image.AGF

  # Convert with custom output
  agetools bmp2agf image.BMP output.AGF -r original/image.AGF

  # Convert directory
  agetools bmp2agf BMP_folder/ -o AGF_output/ -r original_AGF/`,
	Args: cobra.MinimumNArgs(1),
	RunE: runBmp2Agf,
}

func init() {
	rootCmd.AddCommand(bmp2agfCmd)

	bmp2agfCmd.Flags().StringVarP(&bmp2agfOutput, "output", "o", "",
		"output file or directory")
	bmp2agfCmd.Flags().StringVarP(&bmp2agfOriginal, "reference", "r", "",
		"original AGF file or directory for format reference")
	bmp2agfCmd.Flags().BoolVarP(&bmp2agfVerbose, "verbose", "v", false,
		"print verbose progress information")
}

func runBmp2Agf(cmd *cobra.Command, args []string) error {
	input := args[0]

	info, err := os.Stat(input)
	if err != nil {
		return fmt.Errorf("input not found: %s", input)
	}

	if info.IsDir() {
		return convertBmpDirectory(input, bmp2agfOutput, bmp2agfOriginal)
	}

	// Single file
	output := bmp2agfOutput
	if output == "" {
		if len(args) > 1 {
			output = args[1]
		} else {
			output = strings.TrimSuffix(input, filepath.Ext(input)) + ".AGF"
		}
	}

	// Find original AGF
	original := bmp2agfOriginal
	if original == "" {
		// Try same name with .AGF extension in same directory
		original = strings.TrimSuffix(input, filepath.Ext(input)) + ".AGF"
		if _, err := os.Stat(original); os.IsNotExist(err) {
			return fmt.Errorf("original AGF not found, use -r to specify: %s", original)
		}
	}

	return convertBmpFile(input, output, original)
}

func convertBmpFile(input, output, original string) error {
	if bmp2agfVerbose {
		fmt.Printf("Converting %s -> %s (ref: %s)\n", input, output, original)
	}

	if err := agf.Pack(input, original, output, agf.PackOptions{}); err != nil {
		return fmt.Errorf("failed to pack %s: %w", input, err)
	}

	if !bmp2agfVerbose {
		fmt.Printf("Converted: %s\n", filepath.Base(output))
	}

	return nil
}

func convertBmpDirectory(inputDir, outputDir, originalDir string) error {
	if outputDir == "" {
		outputDir = inputDir + "_AGF"
	}

	if originalDir == "" {
		originalDir = inputDir
	}

	if err := os.MkdirAll(outputDir, 0755); err != nil {
		return fmt.Errorf("failed to create output directory: %w", err)
	}

	count := 0
	err := filepath.Walk(inputDir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}

		if info.IsDir() {
			return nil
		}

		ext := strings.ToUpper(filepath.Ext(path))
		if ext != ".BMP" {
			return nil
		}

		// Preserve directory structure
		relPath, _ := filepath.Rel(inputDir, path)
		baseName := strings.TrimSuffix(relPath, filepath.Ext(relPath))
		outPath := filepath.Join(outputDir, baseName+".AGF")
		origPath := filepath.Join(originalDir, baseName+".AGF")

		// Check if original exists
		if _, err := os.Stat(origPath); os.IsNotExist(err) {
			fmt.Fprintf(os.Stderr, "Warning: original AGF not found for %s\n", path)
			return nil
		}

		// Create subdirectories if needed
		if err := os.MkdirAll(filepath.Dir(outPath), 0755); err != nil {
			return err
		}

		if err := convertBmpFile(path, outPath, origPath); err != nil {
			fmt.Fprintf(os.Stderr, "Warning: %v\n", err)
			return nil
		}

		count++
		return nil
	})

	if err != nil {
		return err
	}

	fmt.Printf("Converted %d files\n", count)
	return nil
}
