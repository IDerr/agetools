package cmd

import (
	"fmt"
	"os"
	"path/filepath"

	"agetools/pkg/alf"
	"github.com/spf13/cobra"
)

var sys5iniDumpCmd = &cobra.Command{
	Use:   "sys5ini-dump <sys5ini.bin>",
	Short: "Display SYS5INI.BIN archive structure",
	Long: `Display the structure of SYS5INI.BIN archive index file.

Shows:
  - Archive format version and signature
  - List of referenced DATA*.ALF files
  - File entries with their locations and sizes

Examples:
  # Display SYS5INI.BIN structure
  agetools sys5ini-dump SYS5INI.BIN

  # Display with detailed file list
  agetools sys5ini-dump ../../game/SYS5INI.BIN`,
	Args: cobra.ExactArgs(1),
	RunE: runSys5iniDump,
}

func init() {
	rootCmd.AddCommand(sys5iniDumpCmd)
}

func runSys5iniDump(cmd *cobra.Command, args []string) error {
	archivePath := args[0]

	// Resolve to absolute path
	absPath, err := filepath.Abs(archivePath)
	if err != nil {
		return fmt.Errorf("failed to resolve path: %w", err)
	}

	// Check file exists
	if _, err := os.Stat(absPath); os.IsNotExist(err) {
		return fmt.Errorf("file not found: %s", archivePath)
	}

	// Read the file
	data, err := os.ReadFile(absPath)
	if err != nil {
		return fmt.Errorf("failed to read file: %w", err)
	}

	// Parse metadata without opening ALF files
	header, archiveNames, entries, err := alf.ParseSYS5Metadata(data)
	if err != nil {
		return fmt.Errorf("failed to parse metadata: %w", err)
	}

	// Print header info
	fmt.Printf("File: %s\n", filepath.Base(archivePath))
	fmt.Printf("Format: S%d (%s)\n", header.Version, header.Signature)
	if header.Title != "" {
		fmt.Printf("Title: %s\n", header.Title)
	}
	fmt.Printf("Compressed: %v\n", header.IsCompressed())
	fmt.Println()

	// Print archive sources
	fmt.Printf("Archives (%d):\n", len(archiveNames))
	for i, name := range archiveNames {
		fmt.Printf("  [%d] %s\n", i, name)
	}
	fmt.Println()

	// Print file entries summary
	fmt.Printf("Files: %d total\n", len(entries))

	// Group files by archive
	filesByArchive := make(map[uint32]int)
	for _, entry := range entries {
		filesByArchive[entry.ArchiveIndex]++
	}

	for i := uint32(0); i < uint32(len(archiveNames)); i++ {
		if count, ok := filesByArchive[i]; ok {
			fmt.Printf("  %s: %d files\n", archiveNames[i], count)
		}
	}
	fmt.Println()

	// Print first 20 files as sample
	fmt.Println("Sample files (first 20):")
	for i, entry := range entries {
		if i >= 20 {
			fmt.Printf("  ... and %d more files\n", len(entries)-20)
			break
		}
		archiveName := "UNKNOWN"
		if int(entry.ArchiveIndex) < len(archiveNames) {
			archiveName = archiveNames[entry.ArchiveIndex]
		}
		fmt.Printf("  [%d] %s (archive: %s, offset: 0x%X, size: %d bytes)\n",
			entry.FileIndex, entry.Filename,
			archiveName,
			entry.Offset, entry.Length)
	}

	return nil
}
