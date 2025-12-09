package alf

import (
	"encoding/binary"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/agetools/pkg/lzss"
)

// ExtractOptions configures the extraction process.
type ExtractOptions struct {
	Filter    string // Only extract files containing this string (case-insensitive)
	OutputDir string // Output directory (default: "data")
	Verbose   bool   // Print detailed progress
}

// Extractor handles ALF archive extraction.
type Extractor struct {
	archive *Archive
	opts    ExtractOptions
	baseDir string // Directory containing the archive files
}

// NewExtractor creates a new extractor for the given archive file.
func NewExtractor(archivePath string, opts ExtractOptions) (*Extractor, error) {
	if opts.OutputDir == "" {
		opts.OutputDir = "data"
	}

	return &Extractor{
		opts:    opts,
		baseDir: filepath.Dir(archivePath),
	}, nil
}

// Open opens and parses the archive file.
func (e *Extractor) Open(archivePath string) error {
	data, err := os.ReadFile(archivePath)
	if err != nil {
		return fmt.Errorf("failed to read archive: %w", err)
	}

	if len(data) < 8 {
		return fmt.Errorf("file too small to be a valid archive")
	}

	// Detect format version
	version, err := DetectFormat(data)
	if err != nil {
		return fmt.Errorf("failed to detect format: %w", err)
	}

	e.archive = &Archive{
		FilePath: archivePath,
	}

	switch version {
	case FormatS4:
		return e.openS4(data)
	case FormatS5:
		return e.openS5(data)
	default:
		return ErrNotSupported
	}
}

// openS4 parses S4 format archives (S4IC/S4AC).
func (e *Extractor) openS4(data []byte) error {
	header, err := ReadS4Header(data)
	if err != nil {
		return fmt.Errorf("failed to read S4 header: %w", err)
	}
	e.archive.Header = *header

	if !header.IsCompressed() {
		return fmt.Errorf("S4 uncompressed format not supported (only S4IC/S4AC)")
	}

	// For S4AC (append), metadata starts at different offset
	metadataOffset := S4HeaderSize
	if header.IsAppend() {
		metadataOffset = 0x10C // 268 bytes
	}

	// Read sector header
	sectHdr, err := ReadS4SectorHeader(data, metadataOffset)
	if err != nil {
		return fmt.Errorf("failed to read sector header: %w", err)
	}

	// Extract compressed data
	compStart := metadataOffset + 12
	compEnd := compStart + int(sectHdr.Length)
	if compEnd > len(data) {
		return fmt.Errorf("compressed data exceeds file size")
	}

	compData := data[compStart:compEnd]

	// Decompress if needed
	var metadata []byte
	if sectHdr.OriginalLength != sectHdr.Length {
		metadata = lzss.Decompress(compData)
		if len(metadata) == 0 {
			return fmt.Errorf("LZSS decompression failed: empty result")
		}
	} else {
		metadata = compData
	}

	return e.parseS4Metadata(metadata)
}

// openS5 parses S5 format archives (S5IN/S5IC/S5AC).
func (e *Extractor) openS5(data []byte) error {
	header, err := ReadS5Header(data)
	if err != nil {
		return fmt.Errorf("failed to read S5 header: %w", err)
	}
	e.archive.Header = *header

	if header.IsCompressed() {
		return e.parseS5Compressed(data)
	}
	return e.parseS5Uncompressed(data)
}

// parseS4Metadata parses the decompressed metadata from S4IC/S4AC.
func (e *Extractor) parseS4Metadata(metadata []byte) error {
	pos := 0

	// Read archive count
	if pos+4 > len(metadata) {
		return io.ErrUnexpectedEOF
	}
	arcCount := binary.LittleEndian.Uint32(metadata[pos:])
	pos += 4

	// Read archive names and open handles
	for i := uint32(0); i < arcCount; i++ {
		if pos+S4ArchiveEntrySize > len(metadata) {
			return io.ErrUnexpectedEOF
		}

		// S4 uses UTF-8 for archive names
		arcName := readNullTerminatedString(metadata[pos : pos+S4ArchiveEntrySize])
		pos += S4ArchiveEntrySize

		arcPath := filepath.Join(e.baseDir, arcName)
		handle, err := os.Open(arcPath)
		if err != nil {
			return fmt.Errorf("failed to open archive %s: %w", arcName, err)
		}

		e.archive.Sources = append(e.archive.Sources, ArchiveSource{
			Name:   arcName,
			Path:   arcPath,
			Handle: handle,
		})
	}

	// Read entry count
	if pos+4 > len(metadata) {
		return io.ErrUnexpectedEOF
	}
	entryCount := binary.LittleEndian.Uint32(metadata[pos:])
	pos += 4

	// Read file entries (S4: 80 bytes each)
	for i := uint32(0); i < entryCount; i++ {
		if pos+S4FileEntrySize > len(metadata) {
			return io.ErrUnexpectedEOF
		}

		// S4 file entry: 64 bytes filename (UTF-8), then 4x uint32
		filename := readNullTerminatedString(metadata[pos : pos+64])
		arcIndex := binary.LittleEndian.Uint32(metadata[pos+64:])
		fileIndex := binary.LittleEndian.Uint32(metadata[pos+68:])
		offset := binary.LittleEndian.Uint32(metadata[pos+72:])
		length := binary.LittleEndian.Uint32(metadata[pos+76:])

		e.archive.Entries = append(e.archive.Entries, FileEntry{
			Filename:     filename,
			ArchiveIndex: arcIndex,
			FileIndex:    fileIndex,
			Offset:       offset,
			Length:       length,
		})

		pos += S4FileEntrySize
	}

	return nil
}

// parseS5Compressed parses S5IC/S5AC compressed archives.
func (e *Extractor) parseS5Compressed(data []byte) error {
	// Offset differs for append vs normal
	infoOffset := 0x21C
	if e.archive.Header.IsAppend() {
		infoOffset = 0x214
	}

	compInfo, err := ReadCompressionInfo(data, infoOffset)
	if err != nil {
		return fmt.Errorf("failed to read compression info: %w", err)
	}

	// Extract and decompress metadata
	compStart := infoOffset + 12
	compEnd := compStart + int(compInfo.CompSize)
	if compEnd > len(data) {
		return fmt.Errorf("compressed data exceeds file size")
	}

	compData := data[compStart:compEnd]
	metadata := lzss.Decompress(compData)
	if len(metadata) == 0 {
		return fmt.Errorf("LZSS decompression failed: empty result")
	}

	return e.parseS5Metadata(metadata)
}

// parseS5Uncompressed parses S5IN uncompressed archives.
func (e *Extractor) parseS5Uncompressed(data []byte) error {
	pos := 0x200

	// Read archive name
	arcName := ReadUTF16StringPadded(data, pos, 0x200)
	arcName = strings.TrimRight(arcName, "\x00")
	pos += 0x200

	// Read entry count
	if pos+4 > len(data) {
		return io.ErrUnexpectedEOF
	}
	entryCount := binary.LittleEndian.Uint32(data[pos:])
	pos += 4

	// Open the archive file
	arcPath := filepath.Join(e.baseDir, arcName)
	handle, err := os.Open(arcPath)
	if err != nil {
		return fmt.Errorf("failed to open archive %s: %w", arcName, err)
	}

	e.archive.Sources = append(e.archive.Sources, ArchiveSource{
		Name:   arcName,
		Path:   arcPath,
		Handle: handle,
	})

	// Read entries
	for i := uint32(0); i < entryCount; i++ {
		if pos+S5FileEntrySize > len(data) {
			return io.ErrUnexpectedEOF
		}

		filename := ReadUTF16StringPadded(data, pos, 0x88)
		filename = strings.TrimRight(filename, "\x00")

		offset := binary.LittleEndian.Uint32(data[pos+0x88:])
		length := binary.LittleEndian.Uint32(data[pos+0x8C:])

		e.archive.Entries = append(e.archive.Entries, FileEntry{
			Filename:     filename,
			ArchiveIndex: 0,
			FileIndex:    i,
			Offset:       offset,
			Length:       length,
		})

		pos += S5FileEntrySize
	}

	return nil
}

// parseS5Metadata parses the decompressed metadata from S5IC/S5AC.
func (e *Extractor) parseS5Metadata(metadata []byte) error {
	pos := 0

	// Read archive count
	if pos+4 > len(metadata) {
		return io.ErrUnexpectedEOF
	}
	arcCount := binary.LittleEndian.Uint32(metadata[pos:])
	pos += 4

	// Read archive names and open handles
	for i := uint32(0); i < arcCount; i++ {
		if pos+S5ArchiveEntrySize > len(metadata) {
			return io.ErrUnexpectedEOF
		}

		// S5 uses UTF-16LE for archive names
		arcName := ReadUTF16StringPadded(metadata, pos, S5ArchiveEntrySize)
		arcName = strings.TrimRight(arcName, "\x00")
		pos += S5ArchiveEntrySize

		arcPath := filepath.Join(e.baseDir, arcName)
		handle, err := os.Open(arcPath)
		if err != nil {
			return fmt.Errorf("failed to open archive %s: %w", arcName, err)
		}

		e.archive.Sources = append(e.archive.Sources, ArchiveSource{
			Name:   arcName,
			Path:   arcPath,
			Handle: handle,
		})
	}

	// Read entry count
	if pos+4 > len(metadata) {
		return io.ErrUnexpectedEOF
	}
	entryCount := binary.LittleEndian.Uint32(metadata[pos:])
	pos += 4

	// Read file entries (S5: 144 bytes each)
	for i := uint32(0); i < entryCount; i++ {
		if pos+S5FileEntrySize > len(metadata) {
			return io.ErrUnexpectedEOF
		}

		// S5 file entry: 128 bytes filename (UTF-16LE), then 4x uint32
		filename := ReadUTF16StringPadded(metadata, pos, 0x80)
		filename = strings.TrimRight(filename, "\x00")

		arcIndex := binary.LittleEndian.Uint32(metadata[pos+0x80:])
		fileIndex := binary.LittleEndian.Uint32(metadata[pos+0x84:])
		offset := binary.LittleEndian.Uint32(metadata[pos+0x88:])
		length := binary.LittleEndian.Uint32(metadata[pos+0x8C:])

		e.archive.Entries = append(e.archive.Entries, FileEntry{
			Filename:     filename,
			ArchiveIndex: arcIndex,
			FileIndex:    fileIndex,
			Offset:       offset,
			Length:       length,
		})

		pos += S5FileEntrySize
	}

	return nil
}

// readNullTerminatedString reads a null-terminated UTF-8 string from data.
func readNullTerminatedString(data []byte) string {
	for i, b := range data {
		if b == 0 {
			return string(data[:i])
		}
	}
	return string(data)
}

// Extract extracts all files from the archive.
func (e *Extractor) Extract() error {
	if e.archive == nil {
		return fmt.Errorf("archive not opened")
	}

	// Group entries by archive for parallel extraction
	groups := make(map[uint32][]FileEntry)
	for _, entry := range e.archive.Entries {
		// Apply filter if set
		if e.opts.Filter != "" {
			if !strings.Contains(strings.ToLower(entry.Filename), strings.ToLower(e.opts.Filter)) {
				continue
			}
		}
		groups[entry.ArchiveIndex] = append(groups[entry.ArchiveIndex], entry)
	}

	var wg sync.WaitGroup
	errChan := make(chan error, len(groups))

	for arcIdx, entries := range groups {
		wg.Add(1)
		go func(idx uint32, files []FileEntry) {
			defer wg.Done()
			if err := e.extractFromArchive(idx, files); err != nil {
				errChan <- err
			}
		}(arcIdx, entries)
	}

	wg.Wait()
	close(errChan)

	// Return first error if any
	for err := range errChan {
		return err
	}

	return nil
}

// extractFromArchive extracts files from a single archive source.
func (e *Extractor) extractFromArchive(arcIdx uint32, entries []FileEntry) error {
	if int(arcIdx) >= len(e.archive.Sources) {
		return fmt.Errorf("archive index %d out of range", arcIdx)
	}

	src := e.archive.Sources[arcIdx]
	arcName := strings.TrimSuffix(src.Name, filepath.Ext(src.Name))
	outDir := filepath.Join(e.opts.OutputDir, arcName)

	// Create output directory
	if err := os.MkdirAll(outDir, 0755); err != nil {
		return fmt.Errorf("failed to create output directory: %w", err)
	}

	for _, entry := range entries {
		outPath := filepath.Join(outDir, entry.Filename)

		// Ensure parent directory exists
		if dir := filepath.Dir(outPath); dir != outDir {
			if err := os.MkdirAll(dir, 0755); err != nil {
				return fmt.Errorf("failed to create directory %s: %w", dir, err)
			}
		}

		if e.opts.Verbose {
			fmt.Printf("\t%s\n", outPath)
		}

		// Read file data from archive
		data := make([]byte, entry.Length)
		if _, err := src.Handle.ReadAt(data, int64(entry.Offset)); err != nil {
			return fmt.Errorf("failed to read %s: %w", entry.Filename, err)
		}

		// Write output file
		if err := os.WriteFile(outPath, data, 0644); err != nil {
			return fmt.Errorf("failed to write %s: %w", outPath, err)
		}
	}

	return nil
}

// Close closes the extractor and all open file handles.
func (e *Extractor) Close() {
	if e.archive != nil {
		e.archive.Close()
	}
}

// GetArchive returns the parsed archive metadata.
func (e *Extractor) GetArchive() *Archive {
	return e.archive
}
