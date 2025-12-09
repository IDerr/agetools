package alf

import (
	"encoding/binary"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"agetools/pkg/lzss"
)

// PackOptions configures the packing process.
type PackOptions struct {
	OutputDir   string        // Output directory for repacked archives
	Version     FormatVersion // Force S4 or S5 format (0 = auto-detect from original)
	Compress    bool          // Whether to compress the metadata (default: true)
	Verbose     bool          // Print detailed progress
	OriginalBIN string        // Path to original SYS5INI.BIN for metadata reference
}

// Packer handles ALF archive packing.
type Packer struct {
	opts       PackOptions
	original   *Archive  // Original archive for reference
	inputDir   string    // Directory containing files to pack
	version    FormatVersion
}

// NewPacker creates a new packer.
func NewPacker(inputDir string, opts PackOptions) (*Packer, error) {
	if opts.OutputDir == "" {
		opts.OutputDir = "."
	}

	return &Packer{
		opts:     opts,
		inputDir: inputDir,
	}, nil
}

// LoadOriginal loads the original archive for reference.
func (p *Packer) LoadOriginal(archivePath string) error {
	extractor, err := NewExtractor(archivePath, ExtractOptions{})
	if err != nil {
		return err
	}

	if err := extractor.Open(archivePath); err != nil {
		extractor.Close()
		return err
	}

	p.original = extractor.GetArchive()
	p.version = p.original.Header.Version

	// Close file handles but keep metadata
	for i := range p.original.Sources {
		if p.original.Sources[i].Handle != nil {
			p.original.Sources[i].Handle.Close()
			p.original.Sources[i].Handle = nil
		}
	}

	return nil
}

// Pack repacks the files into ALF archives.
func (p *Packer) Pack() error {
	if p.original == nil {
		return fmt.Errorf("original archive not loaded - call LoadOriginal first")
	}

	// Collect all files from input directory, organized by archive
	filesByArchive := make(map[int][]packedFile)

	for i, src := range p.original.Sources {
		arcName := strings.TrimSuffix(src.Name, filepath.Ext(src.Name))
		srcDir := filepath.Join(p.inputDir, arcName)

		if _, err := os.Stat(srcDir); os.IsNotExist(err) {
			if p.opts.Verbose {
				fmt.Printf("Warning: Directory %s not found, using original archive\n", srcDir)
			}
			continue
		}

		filesByArchive[i] = []packedFile{}
	}

	// Build list of files to pack for each archive
	for _, entry := range p.original.Entries {
		arcIdx := int(entry.ArchiveIndex)
		arcName := strings.TrimSuffix(p.original.Sources[arcIdx].Name, filepath.Ext(p.original.Sources[arcIdx].Name))
		filePath := filepath.Join(p.inputDir, arcName, entry.Filename)

		pf := packedFile{
			name:      entry.Filename,
			arcIndex:  entry.ArchiveIndex,
			fileIndex: entry.FileIndex,
		}

		if info, err := os.Stat(filePath); err == nil {
			pf.path = filePath
			pf.size = uint32(info.Size())
			pf.modified = true
		} else {
			// Use original file
			pf.origOffset = entry.Offset
			pf.origLength = entry.Length
			pf.size = entry.Length
		}

		filesByArchive[arcIdx] = append(filesByArchive[arcIdx], pf)
	}

	// Sort files by original index within each archive
	for arcIdx := range filesByArchive {
		sort.Slice(filesByArchive[arcIdx], func(i, j int) bool {
			return filesByArchive[arcIdx][i].fileIndex < filesByArchive[arcIdx][j].fileIndex
		})
	}

	// Create output ALF files
	newEntries := make([]FileEntry, 0, len(p.original.Entries))

	for arcIdx, src := range p.original.Sources {
		files := filesByArchive[arcIdx]
		if len(files) == 0 {
			continue
		}

		outPath := filepath.Join(p.opts.OutputDir, src.Name)
		if p.opts.Verbose {
			fmt.Printf("Creating %s\n", outPath)
		}

		// Open original archive for reading unmodified files
		origPath := filepath.Join(filepath.Dir(p.opts.OriginalBIN), src.Name)
		origFile, err := os.Open(origPath)
		if err != nil {
			return fmt.Errorf("failed to open original archive %s: %w", origPath, err)
		}

		outFile, err := os.Create(outPath)
		if err != nil {
			origFile.Close()
			return fmt.Errorf("failed to create output archive %s: %w", outPath, err)
		}

		var offset uint32 = 0
		for i := range files {
			pf := &files[i]

			if pf.modified {
				// Read from modified file
				data, err := os.ReadFile(pf.path)
				if err != nil {
					outFile.Close()
					origFile.Close()
					return fmt.Errorf("failed to read %s: %w", pf.path, err)
				}

				if _, err := outFile.Write(data); err != nil {
					outFile.Close()
					origFile.Close()
					return fmt.Errorf("failed to write to archive: %w", err)
				}

				if p.opts.Verbose {
					fmt.Printf("  + %s (modified)\n", pf.name)
				}
			} else {
				// Copy from original archive
				data := make([]byte, pf.origLength)
				if _, err := origFile.ReadAt(data, int64(pf.origOffset)); err != nil {
					outFile.Close()
					origFile.Close()
					return fmt.Errorf("failed to read from original: %w", err)
				}

				if _, err := outFile.Write(data); err != nil {
					outFile.Close()
					origFile.Close()
					return fmt.Errorf("failed to write to archive: %w", err)
				}
			}

			newEntries = append(newEntries, FileEntry{
				Filename:     pf.name,
				ArchiveIndex: pf.arcIndex,
				FileIndex:    pf.fileIndex,
				Offset:       offset,
				Length:       pf.size,
			})

			offset += pf.size
		}

		origFile.Close()
		outFile.Close()
	}

	// Sort entries by archive index then file index
	sort.Slice(newEntries, func(i, j int) bool {
		if newEntries[i].ArchiveIndex != newEntries[j].ArchiveIndex {
			return newEntries[i].ArchiveIndex < newEntries[j].ArchiveIndex
		}
		return newEntries[i].FileIndex < newEntries[j].FileIndex
	})

	// Create new index file (SYS5INI.BIN or similar)
	return p.writeIndexFile(newEntries)
}

// writeIndexFile writes the archive index file.
func (p *Packer) writeIndexFile(entries []FileEntry) error {
	outPath := filepath.Join(p.opts.OutputDir, filepath.Base(p.original.FilePath))
	if p.opts.Verbose {
		fmt.Printf("Creating index file %s\n", outPath)
	}

	// Build metadata
	var metadata []byte

	if p.version == FormatS5 {
		metadata = p.buildS5Metadata(entries)
	} else {
		metadata = p.buildS4Metadata(entries)
	}

	// Compress metadata
	compressed := lzss.Compress(metadata)

	// Build full file
	var buf []byte

	if p.version == FormatS5 {
		buf = p.buildS5IndexFile(metadata, compressed)
	} else {
		buf = p.buildS4IndexFile(metadata, compressed)
	}

	return os.WriteFile(outPath, buf, 0644)
}

// buildS5Metadata builds the uncompressed metadata for S5 format.
func (p *Packer) buildS5Metadata(entries []FileEntry) []byte {
	arcCount := len(p.original.Sources)
	entryCount := len(entries)

	// Calculate size: 4 + (arcCount * 512) + 4 + (entryCount * 144)
	size := 4 + (arcCount * S5ArchiveEntrySize) + 4 + (entryCount * S5FileEntrySize)
	buf := make([]byte, size)
	pos := 0

	// Archive count
	binary.LittleEndian.PutUint32(buf[pos:], uint32(arcCount))
	pos += 4

	// Archive names
	for _, src := range p.original.Sources {
		encoded := EncodeUTF16LE(src.Name)
		copy(buf[pos:], encoded)
		pos += S5ArchiveEntrySize
	}

	// Entry count
	binary.LittleEndian.PutUint32(buf[pos:], uint32(entryCount))
	pos += 4

	// File entries
	for _, entry := range entries {
		// Filename (128 bytes UTF-16LE)
		encoded := EncodeUTF16LE(entry.Filename)
		copy(buf[pos:], encoded)

		// Metadata at offset 0x80
		binary.LittleEndian.PutUint32(buf[pos+0x80:], entry.ArchiveIndex)
		binary.LittleEndian.PutUint32(buf[pos+0x84:], entry.FileIndex)
		binary.LittleEndian.PutUint32(buf[pos+0x88:], entry.Offset)
		binary.LittleEndian.PutUint32(buf[pos+0x8C:], entry.Length)

		pos += S5FileEntrySize
	}

	return buf
}

// buildS4Metadata builds the uncompressed metadata for S4 format.
func (p *Packer) buildS4Metadata(entries []FileEntry) []byte {
	arcCount := len(p.original.Sources)
	entryCount := len(entries)

	// Calculate size: 4 + (arcCount * 256) + 4 + (entryCount * 80)
	size := 4 + (arcCount * S4ArchiveEntrySize) + 4 + (entryCount * S4FileEntrySize)
	buf := make([]byte, size)
	pos := 0

	// Archive count
	binary.LittleEndian.PutUint32(buf[pos:], uint32(arcCount))
	pos += 4

	// Archive names
	for _, src := range p.original.Sources {
		copy(buf[pos:], []byte(src.Name))
		pos += S4ArchiveEntrySize
	}

	// Entry count
	binary.LittleEndian.PutUint32(buf[pos:], uint32(entryCount))
	pos += 4

	// File entries
	for _, entry := range entries {
		// Filename (64 bytes UTF-8)
		copy(buf[pos:], []byte(entry.Filename))

		// Metadata at offset 0x40
		binary.LittleEndian.PutUint32(buf[pos+0x40:], entry.ArchiveIndex)
		binary.LittleEndian.PutUint32(buf[pos+0x44:], entry.FileIndex)
		binary.LittleEndian.PutUint32(buf[pos+0x48:], entry.Offset)
		binary.LittleEndian.PutUint32(buf[pos+0x4C:], entry.Length)

		pos += S4FileEntrySize
	}

	return buf
}

// buildS5IndexFile builds the complete S5 index file with header and compressed data.
func (p *Packer) buildS5IndexFile(metadata, compressed []byte) []byte {
	// Header (540 bytes) + CompressionInfo (12 bytes) + compressed data
	size := S5HeaderSize + 12 + len(compressed)
	buf := make([]byte, size)

	// Copy original header
	if p.original.Header.RawS5 != nil {
		copy(buf[0:480], p.original.Header.RawS5.Signature[:])
		copy(buf[480:540], p.original.Header.RawS5.Unknown[:])
	}

	// Compression info at 0x21C
	pos := S5HeaderSize
	binary.LittleEndian.PutUint32(buf[pos:], uint32(len(metadata)))   // Uncompressed size 1
	binary.LittleEndian.PutUint32(buf[pos+4:], uint32(len(metadata))) // Uncompressed size 2
	binary.LittleEndian.PutUint32(buf[pos+8:], uint32(len(compressed))) // Compressed size
	pos += 12

	// Compressed data
	copy(buf[pos:], compressed)

	return buf
}

// buildS4IndexFile builds the complete S4 index file with header and compressed data.
func (p *Packer) buildS4IndexFile(metadata, compressed []byte) []byte {
	// Header (300 bytes) + SectorHeader (12 bytes) + compressed data
	size := S4HeaderSize + 12 + len(compressed)
	buf := make([]byte, size)

	// Copy original header
	if p.original.Header.RawS4 != nil {
		copy(buf[0:240], p.original.Header.RawS4.Signature[:])
		copy(buf[240:300], p.original.Header.RawS4.Unknown[:])
	}

	// Sector header at 0x12C
	pos := S4HeaderSize
	binary.LittleEndian.PutUint32(buf[pos:], uint32(len(metadata)))   // Original length
	binary.LittleEndian.PutUint32(buf[pos+4:], uint32(len(metadata))) // Original length 2
	binary.LittleEndian.PutUint32(buf[pos+8:], uint32(len(compressed))) // Compressed length
	pos += 12

	// Compressed data
	copy(buf[pos:], compressed)

	return buf
}

// packedFile represents a file to be packed.
type packedFile struct {
	name       string
	path       string // Path to modified file (empty if using original)
	arcIndex   uint32
	fileIndex  uint32
	size       uint32
	origOffset uint32 // Original offset (if not modified)
	origLength uint32 // Original length (if not modified)
	modified   bool
}

// Close cleans up resources.
func (p *Packer) Close() {
	if p.original != nil {
		p.original.Close()
	}
}
