// Package alf handles Eushully ALF/AAI archive extraction.
package alf

import (
	"encoding/binary"
	"io"
	"os"
)

// Format version constants
type FormatVersion int

const (
	FormatS4 FormatVersion = 4 // S4IC/S4AC - UTF-8, 300-byte header
	FormatS5 FormatVersion = 5 // S5IN/S5IC/S5AC - UTF-16LE, 540-byte header
)

// Header size constants
const (
	S4HeaderSize     = 300 // 0x12C bytes
	S5HeaderSize     = 540 // 0x21C bytes
	S4FileEntrySize  = 80  // 0x50 bytes
	S5FileEntrySize  = 144 // 0x90 bytes
	S4ArchiveEntrySize = 256 // 0x100 bytes
	S5ArchiveEntrySize = 512 // 0x200 bytes
)

// S4Header represents the S4 archive header structure.
// Layout (300 bytes total):
//
//	0x00-0xEF: Signature/Title (240 bytes, UTF-8)
//	0xF0-0x12B: Unknown/padding (60 bytes)
type S4Header struct {
	Signature [240]byte // UTF-8 encoded signature + title
	Unknown   [60]byte  // Padding/Unknown
}

// SignatureString returns the decoded signature string (S4IC, S4AC, etc.).
func (h *S4Header) SignatureString() string {
	// Find first null or return full string
	for i, b := range h.Signature[:8] {
		if b == 0 {
			return string(h.Signature[:i])
		}
	}
	return string(h.Signature[:8])
}

// TitleString returns the decoded title string.
func (h *S4Header) TitleString() string {
	// Title follows the signature, find it after first null
	start := 0
	for i, b := range h.Signature {
		if b == 0 {
			start = i + 1
			break
		}
	}
	// Find end of title
	end := start
	for i := start; i < len(h.Signature); i++ {
		if h.Signature[i] == 0 {
			end = i
			break
		}
	}
	if end > start {
		return string(h.Signature[start:end])
	}
	return ""
}

// S5Header represents the S5 archive header structure.
// Layout (540 bytes total):
//
//	0x00-0x1DF: Signature/Title (480 bytes, UTF-16LE)
//	0x1E0-0x21B: Unknown/padding (60 bytes)
type S5Header struct {
	Signature [480]byte // UTF-16LE encoded signature + title
	Unknown   [60]byte  // Padding/Unknown
}

// SignatureString returns the decoded signature string (S5IN, S5IC, S5AC).
func (h *S5Header) SignatureString() string {
	return DecodeUTF16LE(h.Signature[:8])
}

// TitleString returns the decoded title string.
func (h *S5Header) TitleString() string {
	// Title starts at offset 0x10 (16 bytes) in the signature area
	return DecodeUTF16LE(h.Signature[16:])
}

// Header is a unified header interface for both S4 and S5 formats.
type Header struct {
	Version   FormatVersion
	Signature string
	Title     string
	RawS4     *S4Header
	RawS5     *S5Header
}

// IsCompressed returns true if the archive uses LZSS compression.
func (h *Header) IsCompressed() bool {
	return len(h.Signature) >= 4 && h.Signature[3] == 'C'
}

// IsAppend returns true if this is an append archive (S4AC/S5AC).
func (h *Header) IsAppend() bool {
	return len(h.Signature) >= 3 && h.Signature[2] == 'A'
}

// HeaderSize returns the header size for this format version.
func (h *Header) HeaderSize() int {
	if h.Version == FormatS4 {
		return S4HeaderSize
	}
	return S5HeaderSize
}

// S4SectorHeader contains compression metadata for S4 formats.
// Located immediately after the header (at offset 0x12C).
type S4SectorHeader struct {
	OriginalLength  uint32 // Uncompressed size
	OriginalLength2 uint32 // Duplicate/unused
	Length          uint32 // Compressed size
}

// CompressionInfo contains compression metadata for S5IC/S5AC formats.
// Located at offset 0x214 (append) or 0x21C (normal).
type CompressionInfo struct {
	UncompSize1 uint32 // First uncompressed size
	UncompSize2 uint32 // Second uncompressed size
	CompSize    uint32 // Compressed data size
}

// FileEntry represents a single file entry in the archive metadata.
// S4: 80 bytes (0x50) - filename 64 bytes UTF-8
// S5: 144 bytes (0x90) - filename 128 bytes UTF-16LE
type FileEntry struct {
	Filename     string
	ArchiveIndex uint32
	FileIndex    uint32
	Offset       uint32
	Length       uint32
}

// ArchiveSource holds information about a source archive file (the .alf files).
type ArchiveSource struct {
	Name   string   // Archive filename
	Path   string   // Full path to archive
	Handle *os.File // Open file handle
}

// Archive represents a complete ALF archive with all metadata and entries.
type Archive struct {
	Header   Header
	Sources  []ArchiveSource // Source .alf files
	Entries  []FileEntry     // All file entries
	FilePath string          // Path to the index file (SYS4INI.BIN, SYS5INI.BIN, or APPENDxx.AAI)
}

// Close closes all open archive file handles.
func (a *Archive) Close() {
	for _, src := range a.Sources {
		if src.Handle != nil {
			src.Handle.Close()
		}
	}
}

// DetectFormat detects the format version from raw file data.
// Returns FormatS4 or FormatS5 based on the magic bytes.
func DetectFormat(data []byte) (FormatVersion, error) {
	if len(data) < 8 {
		return 0, io.ErrUnexpectedEOF
	}

	// Try S5 first (UTF-16LE): check if bytes 1,3,5,7 are 0x00
	if data[1] == 0 && data[3] == 0 && data[5] == 0 && data[7] == 0 {
		magic := DecodeUTF16LE(data[:8])
		if len(magic) >= 2 && magic[0] == 'S' && magic[1] == '5' {
			return FormatS5, nil
		}
	}

	// Try S4 (UTF-8): direct ASCII check
	if data[0] == 'S' && data[1] == '4' {
		return FormatS4, nil
	}

	return 0, ErrInvalidMagic
}

// ReadS4Header reads an S4 format header from data.
func ReadS4Header(data []byte) (*Header, error) {
	if len(data) < S4HeaderSize {
		return nil, io.ErrUnexpectedEOF
	}

	raw := &S4Header{}
	copy(raw.Signature[:], data[:240])
	copy(raw.Unknown[:], data[240:300])

	return &Header{
		Version:   FormatS4,
		Signature: raw.SignatureString(),
		Title:     raw.TitleString(),
		RawS4:     raw,
	}, nil
}

// ReadS5Header reads an S5 format header from data.
func ReadS5Header(data []byte) (*Header, error) {
	if len(data) < S5HeaderSize {
		return nil, io.ErrUnexpectedEOF
	}

	raw := &S5Header{}
	copy(raw.Signature[:], data[:480])
	copy(raw.Unknown[:], data[480:540])

	return &Header{
		Version:   FormatS5,
		Signature: raw.SignatureString(),
		Title:     raw.TitleString(),
		RawS5:     raw,
	}, nil
}

// ReadS4SectorHeader reads the sector header for S4 compressed formats.
func ReadS4SectorHeader(data []byte, offset int) (*S4SectorHeader, error) {
	if offset+12 > len(data) {
		return nil, io.ErrUnexpectedEOF
	}
	return &S4SectorHeader{
		OriginalLength:  binary.LittleEndian.Uint32(data[offset : offset+4]),
		OriginalLength2: binary.LittleEndian.Uint32(data[offset+4 : offset+8]),
		Length:          binary.LittleEndian.Uint32(data[offset+8 : offset+12]),
	}, nil
}

// ReadCompressionInfo reads compression info for S5 formats from the given offset.
func ReadCompressionInfo(data []byte, offset int) (*CompressionInfo, error) {
	if offset+12 > len(data) {
		return nil, io.ErrUnexpectedEOF
	}
	return &CompressionInfo{
		UncompSize1: binary.LittleEndian.Uint32(data[offset : offset+4]),
		UncompSize2: binary.LittleEndian.Uint32(data[offset+4 : offset+8]),
		CompSize:    binary.LittleEndian.Uint32(data[offset+8 : offset+12]),
	}, nil
}
