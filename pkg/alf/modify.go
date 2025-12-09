package alf

import (
	"encoding/binary"
	"fmt"
	"os"
	"path/filepath"

	"agetools/pkg/lzss"
)

// ParseSYS5Metadata parses SYS5INI.BIN metadata without opening ALF files.
// Returns header, archive names, file entries, and error.
func ParseSYS5Metadata(data []byte) (*Header, []string, []FileEntry, error) {
	// Detect format
	version, err := DetectFormat(data)
	if err != nil {
		return nil, nil, nil, fmt.Errorf("failed to detect format: %w", err)
	}

	if version != FormatS5 {
		return nil, nil, nil, fmt.Errorf("only S5 format supported, got S%d", version)
	}

	// Parse header
	header, err := ReadS5Header(data)
	if err != nil {
		return nil, nil, nil, fmt.Errorf("failed to read header: %w", err)
	}

	if !header.IsCompressed() {
		return nil, nil, nil, fmt.Errorf("uncompressed S5IN format not supported")
	}

	// Parse metadata
	infoOffset := 0x21C
	compInfo, err := ReadCompressionInfo(data, infoOffset)
	if err != nil {
		return nil, nil, nil, fmt.Errorf("failed to read compression info: %w", err)
	}

	compStart := infoOffset + 12
	compEnd := compStart + int(compInfo.CompSize)
	if compEnd > len(data) {
		return nil, nil, nil, fmt.Errorf("compressed data exceeds file size")
	}

	compData := data[compStart:compEnd]
	metadata := lzss.Decompress(compData)
	if len(metadata) == 0 {
		return nil, nil, nil, fmt.Errorf("LZSS decompression failed")
	}

	// Parse metadata content
	pos := 0
	if pos+4 > len(metadata) {
		return nil, nil, nil, fmt.Errorf("metadata too short")
	}
	arcCount := binary.LittleEndian.Uint32(metadata[pos:])
	pos += 4

	// Read archive names
	archiveNames := make([]string, arcCount)
	for i := uint32(0); i < arcCount; i++ {
		if pos+S5ArchiveEntrySize > len(metadata) {
			return nil, nil, nil, fmt.Errorf("metadata truncated at archive %d", i)
		}
		archiveNames[i] = ReadUTF16StringPadded(metadata, pos, S5ArchiveEntrySize)
		pos += S5ArchiveEntrySize
	}

	// Read file count
	if pos+4 > len(metadata) {
		return nil, nil, nil, fmt.Errorf("metadata too short for file count")
	}
	fileCount := binary.LittleEndian.Uint32(metadata[pos:])
	pos += 4

	// Read file entries
	entries := make([]FileEntry, fileCount)
	for i := uint32(0); i < fileCount; i++ {
		if pos+S5FileEntrySize > len(metadata) {
			return nil, nil, nil, fmt.Errorf("metadata truncated at entry %d", i)
		}

		filename := ReadUTF16StringPadded(metadata, pos, 0x80)
		arcIndex := binary.LittleEndian.Uint32(metadata[pos+0x80:])
		fileIndex := binary.LittleEndian.Uint32(metadata[pos+0x84:])
		offset := binary.LittleEndian.Uint32(metadata[pos+0x88:])
		length := binary.LittleEndian.Uint32(metadata[pos+0x8C:])

		entries[i] = FileEntry{
			Filename:     filename,
			ArchiveIndex: arcIndex,
			FileIndex:    fileIndex,
			Offset:       offset,
			Length:       length,
		}
		pos += S5FileEntrySize
	}

	return header, archiveNames, entries, nil
}

// AddArchiveOptions configures adding a new archive.
type AddArchiveOptions struct {
	ArchiveName string   // Name of new archive (e.g., "DATA9.ALF")
	InputDir    string   // Directory containing files to add
	OutputPath  string   // Output path for modified SYS5INI.BIN
	Verbose     bool     // Print progress
}

// AddArchive adds a new archive entry to SYS5INI.BIN and creates the corresponding DATA*.ALF file.
func AddArchive(sys5iniPath string, opts AddArchiveOptions) error {
	// Read original SYS5INI.BIN
	data, err := os.ReadFile(sys5iniPath)
	if err != nil {
		return fmt.Errorf("failed to read SYS5INI.BIN: %w", err)
	}

	// Detect format
	version, err := DetectFormat(data)
	if err != nil {
		return fmt.Errorf("failed to detect format: %w", err)
	}

	if version != FormatS5 {
		return fmt.Errorf("only S5 format (SYS5INI.BIN) is supported, got S%d", version)
	}

	// Parse header
	header, err := ReadS5Header(data)
	if err != nil {
		return fmt.Errorf("failed to read header: %w", err)
	}

	if !header.IsCompressed() {
		return fmt.Errorf("uncompressed S5IN format not supported yet")
	}

	// Parse metadata
	infoOffset := 0x21C
	compInfo, err := ReadCompressionInfo(data, infoOffset)
	if err != nil {
		return fmt.Errorf("failed to read compression info: %w", err)
	}

	compStart := infoOffset + 12
	compEnd := compStart + int(compInfo.CompSize)
	if compEnd > len(data) {
		return fmt.Errorf("compressed data exceeds file size")
	}

	compData := data[compStart:compEnd]
	metadata := lzss.Decompress(compData)
	if len(metadata) == 0 {
		return fmt.Errorf("LZSS decompression failed")
	}

	// Parse existing metadata
	pos := 0
	if pos+4 > len(metadata) {
		return fmt.Errorf("metadata too short")
	}
	arcCount := binary.LittleEndian.Uint32(metadata[pos:])
	pos += 4

	// Read existing archive names
	existingArchives := make([]string, arcCount)
	for i := uint32(0); i < arcCount; i++ {
		if pos+S5ArchiveEntrySize > len(metadata) {
			return fmt.Errorf("metadata truncated at archive %d", i)
		}
		existingArchives[i] = ReadUTF16StringPadded(metadata, pos, S5ArchiveEntrySize)
		pos += S5ArchiveEntrySize
	}

	// Read file count
	if pos+4 > len(metadata) {
		return fmt.Errorf("metadata too short for file count")
	}
	fileCount := binary.LittleEndian.Uint32(metadata[pos:])
	pos += 4

	// Read existing file entries
	existingEntries := make([]FileEntry, fileCount)
	for i := uint32(0); i < fileCount; i++ {
		if pos+S5FileEntrySize > len(metadata) {
			return fmt.Errorf("metadata truncated at entry %d", i)
		}

		filename := ReadUTF16StringPadded(metadata, pos, 0x80)
		arcIndex := binary.LittleEndian.Uint32(metadata[pos+0x80:])
		fileIndex := binary.LittleEndian.Uint32(metadata[pos+0x84:])
		offset := binary.LittleEndian.Uint32(metadata[pos+0x88:])
		length := binary.LittleEndian.Uint32(metadata[pos+0x8C:])

		existingEntries[i] = FileEntry{
			Filename:     filename,
			ArchiveIndex: arcIndex,
			FileIndex:    fileIndex,
			Offset:       offset,
			Length:       length,
		}
		pos += S5FileEntrySize
	}

	// Collect new files from input directory
	newFiles, err := collectFilesFromDir(opts.InputDir)
	if err != nil {
		return fmt.Errorf("failed to collect files: %w", err)
	}

	if len(newFiles) == 0 {
		return fmt.Errorf("no files found in %s", opts.InputDir)
	}

	// Create new DATA*.ALF file
	newArchiveIndex := uint32(len(existingArchives))
	alfPath := filepath.Join(filepath.Dir(opts.OutputPath), opts.ArchiveName)

	if opts.Verbose {
		fmt.Printf("Creating %s with %d files\n", opts.ArchiveName, len(newFiles))
	}

	newFileEntries, err := createALFArchive(alfPath, newFiles, opts.InputDir, newArchiveIndex, opts.Verbose)
	if err != nil {
		return fmt.Errorf("failed to create ALF: %w", err)
	}

	// Build new metadata
	newMetadata := buildNewMetadata(existingArchives, opts.ArchiveName, existingEntries, newFileEntries)

	// Compress new metadata
	compressedMetadata := lzss.Compress(newMetadata)

	// Build new SYS5INI.BIN
	// Need space for header (540 bytes) + compression info (12 bytes)
	newSys5ini := make([]byte, infoOffset+12)
	copy(newSys5ini, data[:S5HeaderSize])

	// Write compression info
	binary.LittleEndian.PutUint32(newSys5ini[infoOffset:], uint32(len(newMetadata)))
	binary.LittleEndian.PutUint32(newSys5ini[infoOffset+4:], uint32(len(newMetadata)))
	binary.LittleEndian.PutUint32(newSys5ini[infoOffset+8:], uint32(len(compressedMetadata)))

	// Write compressed data
	newSys5ini = append(newSys5ini[:infoOffset+12], compressedMetadata...)

	// Write output
	if err := os.WriteFile(opts.OutputPath, newSys5ini, 0644); err != nil {
		return fmt.Errorf("failed to write output: %w", err)
	}

	if opts.Verbose {
		fmt.Printf("Created modified SYS5INI.BIN: %s\n", opts.OutputPath)
		fmt.Printf("Archives: %d -> %d\n", len(existingArchives), len(existingArchives)+1)
		fmt.Printf("Files: %d -> %d\n", len(existingEntries), len(existingEntries)+len(newFiles))
	}

	return nil
}

// collectFilesFromDir collects all files from a directory.
func collectFilesFromDir(dir string) ([]string, error) {
	var files []string
	err := filepath.Walk(dir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if !info.IsDir() {
			relPath, err := filepath.Rel(dir, path)
			if err != nil {
				return err
			}
			files = append(files, relPath)
		}
		return nil
	})
	return files, err
}

// createALFArchive creates a simple uncompressed ALF file and returns file entries.
func createALFArchive(path string, files []string, inputDir string, archiveIndex uint32, verbose bool) ([]FileEntry, error) {
	f, err := os.Create(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	var entries []FileEntry
	offset := uint32(0)

	for fileIndex, filename := range files {
		filePath := filepath.Join(inputDir, filename)
		data, err := os.ReadFile(filePath)
		if err != nil {
			return nil, fmt.Errorf("failed to read %s: %w", filename, err)
		}

		if _, err := f.Write(data); err != nil {
			return nil, err
		}

		entry := FileEntry{
			Filename:     filename,
			ArchiveIndex: archiveIndex,
			FileIndex:    uint32(fileIndex),
			Offset:       offset,
			Length:       uint32(len(data)),
		}
		entries = append(entries, entry)

		if verbose {
			fmt.Printf("  Added: %s (offset: 0x%X, size: %d)\n", filename, offset, len(data))
		}

		offset += uint32(len(data))
	}

	return entries, nil
}

// buildNewMetadata constructs the new metadata section.
func buildNewMetadata(existingArchives []string, newArchive string, existingEntries []FileEntry, newFileEntries []FileEntry) []byte {
	var buf []byte

	// Write archive count
	arcCount := uint32(len(existingArchives) + 1)
	arcCountBuf := make([]byte, 4)
	binary.LittleEndian.PutUint32(arcCountBuf, arcCount)
	buf = append(buf, arcCountBuf...)

	// Write existing archive names
	for _, name := range existingArchives {
		buf = append(buf, encodeUTF16StringPadded(name, S5ArchiveEntrySize)...)
	}

	// Write new archive name
	buf = append(buf, encodeUTF16StringPadded(newArchive, S5ArchiveEntrySize)...)

	// Write file count
	totalFiles := uint32(len(existingEntries) + len(newFileEntries))
	fileCountBuf := make([]byte, 4)
	binary.LittleEndian.PutUint32(fileCountBuf, totalFiles)
	buf = append(buf, fileCountBuf...)

	// Write existing file entries
	for _, entry := range existingEntries {
		buf = append(buf, encodeFileEntry(entry)...)
	}

	// Write new file entries
	for _, entry := range newFileEntries {
		buf = append(buf, encodeFileEntry(entry)...)
	}

	return buf
}

// encodeFileEntry encodes a file entry to bytes (144 bytes for S5).
func encodeFileEntry(entry FileEntry) []byte {
	buf := make([]byte, S5FileEntrySize)

	// Filename (128 bytes UTF-16LE)
	copy(buf, encodeUTF16StringPadded(entry.Filename, 0x80))

	// Archive index, file index, offset, length
	binary.LittleEndian.PutUint32(buf[0x80:], entry.ArchiveIndex)
	binary.LittleEndian.PutUint32(buf[0x84:], entry.FileIndex)
	binary.LittleEndian.PutUint32(buf[0x88:], entry.Offset)
	binary.LittleEndian.PutUint32(buf[0x8C:], entry.Length)

	return buf
}

// encodeUTF16StringPadded encodes a string to UTF-16LE with padding.
func encodeUTF16StringPadded(s string, size int) []byte {
	buf := make([]byte, size)
	runes := []rune(s)
	for i, r := range runes {
		if i*2+1 >= size {
			break
		}
		binary.LittleEndian.PutUint16(buf[i*2:], uint16(r))
	}
	return buf
}
