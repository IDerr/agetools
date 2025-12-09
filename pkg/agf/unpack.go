package agf

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"io"
	"os"

	"github.com/agetools/pkg/lzss"
)

// UnpackResult contains all data extracted from an AGF file.
type UnpackResult struct {
	Header      *Header
	FileHeader  *BitmapFileHeader
	InfoHeader  *BitmapInfoHeader
	Palette     []RGBQuad
	PixelData   []byte // Raw/encoded pixel data from AGF
	AlphaHeader *AlphaHeader
	AlphaData   []byte // Raw alpha data (only for 32-bit)
	DecodedData []byte // Final RGBA pixel data for output
}

// Unpack reads an AGF file and extracts its contents.
func Unpack(r io.Reader) (*UnpackResult, error) {
	// Read AGF header
	hdr, err := ReadHeader(r)
	if err != nil {
		return nil, err
	}

	if hdr.Type != Type24Bit && hdr.Type != Type32Bit {
		return nil, fmt.Errorf("unsupported AGF type: %d (possibly MPEG)", hdr.Type)
	}

	// Read BMP header sector
	bmpHeaderData, err := readSector(r)
	if err != nil {
		return nil, fmt.Errorf("failed to read BMP header sector: %w", err)
	}

	bmf, bmi, palette, err := ReadBitmapHeaders(bmpHeaderData)
	if err != nil {
		return nil, fmt.Errorf("failed to parse BMP headers: %w", err)
	}

	// Read pixel data sector
	pixelData, err := readSector(r)
	if err != nil {
		return nil, fmt.Errorf("failed to read pixel data sector: %w", err)
	}

	result := &UnpackResult{
		Header:     hdr,
		FileHeader: bmf,
		InfoHeader: bmi,
		Palette:    palette,
		PixelData:  pixelData,
	}

	// Handle 32-bit images with alpha channel
	if hdr.Type == Type32Bit {
		alphaHdr, err := ReadAlphaHeader(r)
		if err != nil {
			return nil, fmt.Errorf("failed to read alpha header: %w", err)
		}
		result.AlphaHeader = alphaHdr

		alphaData, err := readSector(r)
		if err != nil {
			return nil, fmt.Errorf("failed to read alpha sector: %w", err)
		}
		result.AlphaData = alphaData

		// Decode color map with alpha
		result.DecodedData = decodeColorMapWithAlpha(bmi, pixelData, palette, alphaData)
	} else {
		// For 24-bit, decoded data is the pixel data (possibly with palette applied)
		result.DecodedData = pixelData
	}

	return result, nil
}

// UnpackFile unpacks an AGF file from disk.
func UnpackFile(path string) (*UnpackResult, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("failed to open AGF file: %w", err)
	}
	defer f.Close()

	return Unpack(f)
}

// WriteBMP writes the unpacked data as a BMP file.
func (r *UnpackResult) WriteBMP(w io.Writer) error {
	if r.Header.Type == Type32Bit {
		return r.writeBMP32(w)
	}
	return r.writeBMP24(w)
}

// WriteBMPFile writes the unpacked data as a BMP file to disk.
func (r *UnpackResult) WriteBMPFile(path string) error {
	f, err := os.Create(path)
	if err != nil {
		return fmt.Errorf("failed to create BMP file: %w", err)
	}
	defer f.Close()

	return r.WriteBMP(f)
}

// writeBMP32 writes a 32-bit RGBA BMP.
func (r *UnpackResult) writeBMP32(w io.Writer) error {
	width := r.InfoHeader.Width
	height := r.InfoHeader.Height

	// Create new BMP headers for 32-bit output
	bmf := BitmapFileHeader{
		Type:       0x4D42, // "BM"
		OffsetBits: 14 + 40,
	}

	bmi := BitmapInfoHeader{
		Size:     40,
		Width:    width,
		Height:   height,
		Planes:   1,
		BitCount: 32,
	}

	dataSize := int(width) * int(height) * 4
	bmf.Size = uint32(14 + 40 + dataSize)

	// Write headers
	if err := binary.Write(w, binary.LittleEndian, &bmf); err != nil {
		return err
	}
	if err := binary.Write(w, binary.LittleEndian, &bmi); err != nil {
		return err
	}

	// Write pixel data
	_, err := w.Write(r.DecodedData)
	return err
}

// writeBMP24 writes a 24-bit or 8-bit BMP (preserving original format).
func (r *UnpackResult) writeBMP24(w io.Writer) error {
	// Determine if we should include the palette
	// skipPalette = true when bmf.OffsetBits == 54 (no palette in output)
	skipPalette := r.FileHeader.OffsetBits == 54
	paletteSize := 0
	if len(r.Palette) > 0 && !skipPalette {
		paletteSize = len(r.Palette) * 4
	}

	// Create new BMP file header (AGF doesn't store the "BM" signature correctly)
	bmf := BitmapFileHeader{
		Type:       0x4D42, // "BM" signature
		OffsetBits: uint32(14 + 40 + paletteSize),
	}
	bmf.Size = bmf.OffsetBits + uint32(len(r.PixelData))

	// Create info header with correct size
	// The original BMP files have zeros for optional fields
	bmi := BitmapInfoHeader{
		Size:     40,
		Width:    r.InfoHeader.Width,
		Height:   r.InfoHeader.Height,
		Planes:   1,
		BitCount: r.InfoHeader.BitCount,
		// Leave other fields as zero (matching original BMP output)
	}

	// Write file header
	if err := binary.Write(w, binary.LittleEndian, &bmf); err != nil {
		return err
	}

	// Write info header
	if err := binary.Write(w, binary.LittleEndian, &bmi); err != nil {
		return err
	}

	// Write palette if present and not skipped
	if paletteSize > 0 {
		for _, c := range r.Palette {
			if err := binary.Write(w, binary.LittleEndian, &c); err != nil {
				return err
			}
		}
	}

	// Write pixel data
	_, err := w.Write(r.PixelData)
	return err
}

// readSector reads a sector (header + data, with optional LZSS decompression).
func readSector(r io.Reader) ([]byte, error) {
	hdr, err := ReadSectorHeader(r)
	if err != nil {
		return nil, err
	}

	data := make([]byte, hdr.Length)
	if _, err := io.ReadFull(r, data); err != nil {
		return nil, err
	}

	if hdr.IsCompressed() {
		decompressed := lzss.Decompress(data)
		if len(decompressed) != int(hdr.OriginalLength) {
			return nil, fmt.Errorf("decompression size mismatch: got %d, expected %d",
				len(decompressed), hdr.OriginalLength)
		}
		return decompressed, nil
	}

	return data, nil
}

// decodeColorMapWithAlpha combines RGB and Alpha data into RGBA.
// The alpha channel has inverted Y-axis relative to RGB.
func decodeColorMapWithAlpha(bmi *BitmapInfoHeader, encodedData []byte, palette []RGBQuad, alphaData []byte) []byte {
	width := int(bmi.Width)
	height := int(bmi.Height)
	decodedData := make([]byte, width*height*4)

	// RGB stride must be padded to 4 bytes
	rgbStride := (width*int(bmi.BitCount)/8 + 3) &^ 3

	for y := 0; y < height; y++ {
		// Alpha Y is inverted
		alphaLineIndex := (height - y - 1) * width
		rgbaLineIndex := y * width * 4
		rgbLineIndex := y * rgbStride

		for x := 0; x < width; x++ {
			blueIndex := rgbaLineIndex + x*4

			if bmi.BitCount == 8 {
				// Palette-indexed
				palIndex := encodedData[y*rgbStride+x]
				decodedData[blueIndex] = palette[palIndex].Blue
				decodedData[blueIndex+1] = palette[palIndex].Green
				decodedData[blueIndex+2] = palette[palIndex].Red
			} else {
				// 24-bit RGB
				decodedData[blueIndex] = encodedData[rgbLineIndex+x*3]
				decodedData[blueIndex+1] = encodedData[rgbLineIndex+x*3+1]
				decodedData[blueIndex+2] = encodedData[rgbLineIndex+x*3+2]
			}
			decodedData[blueIndex+3] = alphaData[alphaLineIndex+x]
		}
	}

	return decodedData
}

// ReadBMPFile reads a BMP file for packing back to AGF.
func ReadBMPFile(path string) (*BitmapFileHeader, *BitmapInfoHeader, []RGBQuad, []byte, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, nil, nil, nil, fmt.Errorf("failed to read BMP file: %w", err)
	}

	return ReadBMP(bytes.NewReader(data), int64(len(data)))
}

// ReadBMP reads a BMP from a reader.
func ReadBMP(r io.Reader, size int64) (*BitmapFileHeader, *BitmapInfoHeader, []RGBQuad, []byte, error) {
	// Read file header
	bmf := &BitmapFileHeader{}
	if err := binary.Read(r, binary.LittleEndian, bmf); err != nil {
		return nil, nil, nil, nil, fmt.Errorf("failed to read BMP file header: %w", err)
	}

	if bmf.Type != 0x4D42 {
		return nil, nil, nil, nil, fmt.Errorf("invalid BMP signature: %04X", bmf.Type)
	}

	// Read info header
	bmi := &BitmapInfoHeader{}
	if err := binary.Read(r, binary.LittleEndian, bmi); err != nil {
		return nil, nil, nil, nil, fmt.Errorf("failed to read BMP info header: %w", err)
	}

	// Calculate palette size
	paletteOffset := 14 + 40
	paletteSize := int(bmf.OffsetBits) - paletteOffset
	var palette []RGBQuad
	if paletteSize > 0 {
		numColors := paletteSize / 4
		palette = make([]RGBQuad, numColors)
		for i := 0; i < numColors; i++ {
			if err := binary.Read(r, binary.LittleEndian, &palette[i]); err != nil {
				return nil, nil, nil, nil, fmt.Errorf("failed to read palette: %w", err)
			}
		}
	}

	// Read pixel data
	pixelDataSize := size - int64(bmf.OffsetBits)
	pixelData := make([]byte, pixelDataSize)
	if _, err := io.ReadFull(r, pixelData); err != nil {
		return nil, nil, nil, nil, fmt.Errorf("failed to read pixel data: %w", err)
	}

	return bmf, bmi, palette, pixelData, nil
}
