// Package agf handles Eushully AGF image format conversion.
package agf

import (
	"encoding/binary"
	"fmt"
	"io"
)

// AGF type constants
const (
	Type24Bit uint32 = 1 // 24-bit RGB
	Type32Bit uint32 = 2 // 32-bit RGBA (with alpha channel)
)

// Header is the main AGF file header (12 bytes).
type Header struct {
	Signature [4]byte // "ACGF"
	Type      uint32  // 1 = 24-bit, 2 = 32-bit
	Unknown   uint32
}

// SectorHeader is the sector header for compressed data (12 bytes).
type SectorHeader struct {
	OriginalLength  uint32 // Uncompressed size
	OriginalLength2 uint32 // Duplicate (unused)
	Length          uint32 // Compressed size (equals original if uncompressed)
}

// IsCompressed returns true if the sector data is LZSS compressed.
func (h *SectorHeader) IsCompressed() bool {
	return h.Length != h.OriginalLength
}

// AlphaHeader is the alpha channel header for 32-bit images (24 bytes).
type AlphaHeader struct {
	Signature      [4]byte // "ACIF"
	Type           uint32
	Unknown        uint32
	OriginalLength uint32
	Width          uint32
	Height         uint32
}

// BitmapFileHeader is the Windows BMP file header (14 bytes).
type BitmapFileHeader struct {
	Type       uint16 // "BM" = 0x4D42
	Size       uint32 // File size
	Reserved1  uint16
	Reserved2  uint16
	OffsetBits uint32 // Offset to pixel data
}

// BitmapInfoHeader is the Windows BMP info header (40 bytes).
type BitmapInfoHeader struct {
	Size          uint32
	Width         int32
	Height        int32
	Planes        uint16
	BitCount      uint16
	Compression   uint32
	SizeImage     uint32
	XPelsPerMeter int32
	YPelsPerMeter int32
	ClrUsed       uint32
	ClrImportant  uint32
}

// RGBQuad represents a color in the palette (4 bytes).
type RGBQuad struct {
	Blue     byte
	Green    byte
	Red      byte
	Reserved byte
}

// ReadHeader reads an AGF header from a reader.
// Note: Some AGF files don't have the "ACGF" signature, so we only validate the type.
func ReadHeader(r io.Reader) (*Header, error) {
	hdr := &Header{}
	if err := binary.Read(r, binary.LittleEndian, hdr); err != nil {
		return nil, fmt.Errorf("failed to read AGF header: %w", err)
	}
	// Don't validate signature - some files have zeros instead of "ACGF"
	// Only validate that type is valid
	if hdr.Type != Type24Bit && hdr.Type != Type32Bit {
		return nil, fmt.Errorf("unsupported AGF type: %d (possibly MPEG)", hdr.Type)
	}
	return hdr, nil
}

// ReadSectorHeader reads a sector header from a reader.
func ReadSectorHeader(r io.Reader) (*SectorHeader, error) {
	hdr := &SectorHeader{}
	if err := binary.Read(r, binary.LittleEndian, hdr); err != nil {
		return nil, fmt.Errorf("failed to read sector header: %w", err)
	}
	return hdr, nil
}

// ReadAlphaHeader reads an ACIF (alpha channel) header from a reader.
func ReadAlphaHeader(r io.Reader) (*AlphaHeader, error) {
	hdr := &AlphaHeader{}
	if err := binary.Read(r, binary.LittleEndian, hdr); err != nil {
		return nil, fmt.Errorf("failed to read ACIF header: %w", err)
	}
	if string(hdr.Signature[:]) != "ACIF" {
		return nil, fmt.Errorf("invalid ACIF signature: %s", string(hdr.Signature[:]))
	}
	return hdr, nil
}

// ReadBitmapHeaders reads BMP file and info headers from data.
// Note: There's a 2-byte gap between BitmapFileHeader and BitmapInfoHeader in AGF.
func ReadBitmapHeaders(data []byte) (*BitmapFileHeader, *BitmapInfoHeader, []RGBQuad, error) {
	if len(data) < 56 { // 14 + 2 + 40
		return nil, nil, nil, io.ErrUnexpectedEOF
	}

	bmf := &BitmapFileHeader{
		Type:       binary.LittleEndian.Uint16(data[0:2]),
		Size:       binary.LittleEndian.Uint32(data[2:6]),
		Reserved1:  binary.LittleEndian.Uint16(data[6:8]),
		Reserved2:  binary.LittleEndian.Uint16(data[8:10]),
		OffsetBits: binary.LittleEndian.Uint32(data[10:14]),
	}

	// Skip 2-byte padding after BitmapFileHeader
	offset := 16

	bmi := &BitmapInfoHeader{
		Size:          binary.LittleEndian.Uint32(data[offset : offset+4]),
		Width:         int32(binary.LittleEndian.Uint32(data[offset+4 : offset+8])),
		Height:        int32(binary.LittleEndian.Uint32(data[offset+8 : offset+12])),
		Planes:        binary.LittleEndian.Uint16(data[offset+12 : offset+14]),
		BitCount:      binary.LittleEndian.Uint16(data[offset+14 : offset+16]),
		Compression:   binary.LittleEndian.Uint32(data[offset+16 : offset+20]),
		SizeImage:     binary.LittleEndian.Uint32(data[offset+20 : offset+24]),
		XPelsPerMeter: int32(binary.LittleEndian.Uint32(data[offset+24 : offset+28])),
		YPelsPerMeter: int32(binary.LittleEndian.Uint32(data[offset+28 : offset+32])),
		ClrUsed:       binary.LittleEndian.Uint32(data[offset+32 : offset+36]),
		ClrImportant:  binary.LittleEndian.Uint32(data[offset+36 : offset+40]),
	}
	offset += 40

	// Read palette if present
	var palette []RGBQuad
	if len(data) > offset {
		paletteData := data[offset:]
		numColors := len(paletteData) / 4
		palette = make([]RGBQuad, numColors)
		for i := 0; i < numColors; i++ {
			palette[i] = RGBQuad{
				Blue:     paletteData[i*4],
				Green:    paletteData[i*4+1],
				Red:      paletteData[i*4+2],
				Reserved: paletteData[i*4+3],
			}
		}
	}

	return bmf, bmi, palette, nil
}

// WriteBitmapHeaders writes BMP headers to a byte slice (for AGF packing).
// Includes the 2-byte padding between headers.
func WriteBitmapHeaders(bmf *BitmapFileHeader, bmi *BitmapInfoHeader, palette []RGBQuad) []byte {
	size := 14 + 2 + 40 + len(palette)*4
	data := make([]byte, size)

	// BitmapFileHeader (14 bytes)
	binary.LittleEndian.PutUint16(data[0:2], bmf.Type)
	binary.LittleEndian.PutUint32(data[2:6], bmf.Size)
	binary.LittleEndian.PutUint16(data[6:8], bmf.Reserved1)
	binary.LittleEndian.PutUint16(data[8:10], bmf.Reserved2)
	binary.LittleEndian.PutUint32(data[10:14], bmf.OffsetBits)

	// 2-byte padding (data[14:16] already zero)

	// BitmapInfoHeader (40 bytes)
	offset := 16
	binary.LittleEndian.PutUint32(data[offset:offset+4], bmi.Size)
	binary.LittleEndian.PutUint32(data[offset+4:offset+8], uint32(bmi.Width))
	binary.LittleEndian.PutUint32(data[offset+8:offset+12], uint32(bmi.Height))
	binary.LittleEndian.PutUint16(data[offset+12:offset+14], bmi.Planes)
	binary.LittleEndian.PutUint16(data[offset+14:offset+16], bmi.BitCount)
	binary.LittleEndian.PutUint32(data[offset+16:offset+20], bmi.Compression)
	binary.LittleEndian.PutUint32(data[offset+20:offset+24], bmi.SizeImage)
	binary.LittleEndian.PutUint32(data[offset+24:offset+28], uint32(bmi.XPelsPerMeter))
	binary.LittleEndian.PutUint32(data[offset+28:offset+32], uint32(bmi.YPelsPerMeter))
	binary.LittleEndian.PutUint32(data[offset+32:offset+36], bmi.ClrUsed)
	binary.LittleEndian.PutUint32(data[offset+36:offset+40], bmi.ClrImportant)
	offset += 40

	// Palette
	for i, c := range palette {
		data[offset+i*4] = c.Blue
		data[offset+i*4+1] = c.Green
		data[offset+i*4+2] = c.Red
		data[offset+i*4+3] = c.Reserved
	}

	return data
}

// WriteHeader writes an AGF header.
func WriteHeader(w io.Writer, hdr *Header) error {
	return binary.Write(w, binary.LittleEndian, hdr)
}

// WriteSectorHeader writes a sector header.
func WriteSectorHeader(w io.Writer, hdr *SectorHeader) error {
	return binary.Write(w, binary.LittleEndian, hdr)
}

// WriteAlphaHeader writes an ACIF header.
func WriteAlphaHeader(w io.Writer, hdr *AlphaHeader) error {
	return binary.Write(w, binary.LittleEndian, hdr)
}
