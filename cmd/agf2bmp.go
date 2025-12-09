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
	agf2bmpOutput  string
	agf2bmpVerbose bool
)

var agf2bmpCmd = &cobra.Command{
	Use:   "agf2bmp <input> [output]",
	Short: "Convert AGF image to BMP",
	Long: `Convert Eushully AGF image files to BMP format.

Supports both 24-bit and 32-bit AGF files. 32-bit files will be
converted to 32-bit BMP with alpha channel preserved.

Examples:
  # Convert single file
  agetools agf2bmp image.AGF

  # Convert with custom output path
  agetools agf2bmp image.AGF output.BMP

  # Convert directory of AGF files
  agetools agf2bmp AGF_folder/ -o BMP_output/`,
	Args: cobra.MinimumNArgs(1),
	RunE: runAgf2Bmp,
}

func init() {
	rootCmd.AddCommand(agf2bmpCmd)

	agf2bmpCmd.Flags().StringVarP(&agf2bmpOutput, "output", "o", "",
		"output file or directory")
	agf2bmpCmd.Flags().BoolVarP(&agf2bmpVerbose, "verbose", "v", false,
		"print verbose progress information")
}

func runAgf2Bmp(cmd *cobra.Command, args []string) error {
	input := args[0]

	info, err := os.Stat(input)
	if err != nil {
		return fmt.Errorf("input not found: %s", input)
	}

	if info.IsDir() {
		return convertAgfDirectory(input, agf2bmpOutput)
	}

	// Single file
	output := agf2bmpOutput
	if output == "" {
		if len(args) > 1 {
			output = args[1]
		} else {
			output = strings.TrimSuffix(input, filepath.Ext(input)) + ".BMP"
		}
	}

	return convertAgfFile(input, output)
}

func convertAgfFile(input, output string) error {
	if agf2bmpVerbose {
		fmt.Printf("Converting %s -> %s\n", input, output)
	}

	result, err := agf.UnpackFile(input)
	if err != nil {
		return fmt.Errorf("failed to unpack %s: %w", input, err)
	}

	if err := result.WriteBMPFile(output); err != nil {
		return fmt.Errorf("failed to write %s: %w", output, err)
	}

	if !agf2bmpVerbose {
		fmt.Printf("Converted: %s\n", filepath.Base(output))
	}

	return nil
}

func convertAgfDirectory(inputDir, outputDir string) error {
	if outputDir == "" {
		outputDir = inputDir + "_BMP"
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
		if ext != ".AGF" {
			return nil
		}

		// Preserve directory structure
		relPath, _ := filepath.Rel(inputDir, path)
		outPath := filepath.Join(outputDir, strings.TrimSuffix(relPath, filepath.Ext(relPath))+".BMP")

		// Create subdirectories if needed
		if err := os.MkdirAll(filepath.Dir(outPath), 0755); err != nil {
			return err
		}

		if err := convertAgfFile(path, outPath); err != nil {
			fmt.Fprintf(os.Stderr, "Warning: %v\n", err)
			return nil // Continue with other files
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
