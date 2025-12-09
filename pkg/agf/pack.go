package agf

import (
	"bytes"
	"fmt"
	"io"
	"math"
	"os"
)

// PackOptions configures the packing process.
type PackOptions struct {
	Compress bool // Whether to LZSS compress sectors (not implemented yet)
}

// Pack repacks a BMP file into AGF format using the original AGF as reference.
func Pack(bmpPath, agfPath, outputPath string, opts PackOptions) error {
	// First, unpack the original AGF to get format information
	original, err := UnpackFile(agfPath)
	if err != nil {
		return fmt.Errorf("failed to read original AGF: %w", err)
	}

	// Read the BMP file
	_, bmi, _, pixelData, err := ReadBMPFile(bmpPath)
	if err != nil {
		return fmt.Errorf("failed to read BMP: %w", err)
	}

	// Create output file
	f, err := os.Create(outputPath)
	if err != nil {
		return fmt.Errorf("failed to create output file: %w", err)
	}
	defer f.Close()

	// Write AGF header (copy from original)
	if err := WriteHeader(f, original.Header); err != nil {
		return fmt.Errorf("failed to write AGF header: %w", err)
	}

	// Prepare BMP header data for sector
	var sectorPalette []RGBQuad
	if original.InfoHeader.BitCount == 8 {
		sectorPalette = original.Palette
	}
	bmpHeaderData := WriteBitmapHeaders(original.FileHeader, original.InfoHeader, sectorPalette)

	// Write BMP header sector (uncompressed for now)
	if err := writeSector(f, bmpHeaderData); err != nil {
		return fmt.Errorf("failed to write BMP header sector: %w", err)
	}

	// Handle pixel data based on AGF type
	if original.Header.Type == Type32Bit {
		// For 32-bit, we need to separate RGB and Alpha
		encodedData, alphaData := encodeColorMapWithAlpha(pixelData, bmi, original)

		// Write pixel data sector
		if err := writeSector(f, encodedData); err != nil {
			return fmt.Errorf("failed to write pixel sector: %w", err)
		}

		// Write alpha header
		if err := WriteAlphaHeader(f, original.AlphaHeader); err != nil {
			return fmt.Errorf("failed to write alpha header: %w", err)
		}

		// Write alpha sector
		if err := writeSector(f, alphaData); err != nil {
			return fmt.Errorf("failed to write alpha sector: %w", err)
		}
	} else {
		// For 24-bit, encode pixel data
		var encodedData []byte
		if bmi.BitCount == 8 && original.InfoHeader.BitCount == 8 {
			// 8-bit to 8-bit: use as-is but may need palette matching
			encodedData = pixelData
		} else if bmi.BitCount == 8 {
			// 8-bit input, need to expand to match original
			encodedData = pixelData
		} else {
			// Direct copy for matching bit depths
			encodedData = pixelData
		}

		// Write pixel data sector
		if err := writeSector(f, encodedData); err != nil {
			return fmt.Errorf("failed to write pixel sector: %w", err)
		}
	}

	return nil
}

// PackWithReference packs a BMP using pre-loaded original AGF data.
func PackWithReference(bmpPath, outputPath string, original *UnpackResult) error {
	// Read the BMP file
	_, bmi, _, pixelData, err := ReadBMPFile(bmpPath)
	if err != nil {
		return fmt.Errorf("failed to read BMP: %w", err)
	}

	// Create output file
	f, err := os.Create(outputPath)
	if err != nil {
		return fmt.Errorf("failed to create output file: %w", err)
	}
	defer f.Close()

	return packToWriter(f, pixelData, bmi, original)
}

// packToWriter writes packed AGF data to a writer.
func packToWriter(w io.Writer, pixelData []byte, bmi *BitmapInfoHeader, original *UnpackResult) error {
	// Write AGF header (copy from original)
	if err := WriteHeader(w, original.Header); err != nil {
		return fmt.Errorf("failed to write AGF header: %w", err)
	}

	// Prepare BMP header data for sector
	var sectorPalette []RGBQuad
	if original.InfoHeader.BitCount == 8 {
		sectorPalette = original.Palette
	}
	bmpHeaderData := WriteBitmapHeaders(original.FileHeader, original.InfoHeader, sectorPalette)

	// Write BMP header sector
	if err := writeSector(w, bmpHeaderData); err != nil {
		return fmt.Errorf("failed to write BMP header sector: %w", err)
	}

	// Handle pixel data based on AGF type
	if original.Header.Type == Type32Bit {
		encodedData, alphaData := encodeColorMapWithAlpha(pixelData, bmi, original)

		if err := writeSector(w, encodedData); err != nil {
			return fmt.Errorf("failed to write pixel sector: %w", err)
		}

		if err := WriteAlphaHeader(w, original.AlphaHeader); err != nil {
			return fmt.Errorf("failed to write alpha header: %w", err)
		}

		if err := writeSector(w, alphaData); err != nil {
			return fmt.Errorf("failed to write alpha sector: %w", err)
		}
	} else {
		if err := writeSector(w, pixelData); err != nil {
			return fmt.Errorf("failed to write pixel sector: %w", err)
		}
	}

	return nil
}

// PackToBytes packs a BMP to AGF and returns the result as bytes.
func PackToBytes(bmpPath string, original *UnpackResult) ([]byte, error) {
	_, bmi, _, pixelData, err := ReadBMPFile(bmpPath)
	if err != nil {
		return nil, fmt.Errorf("failed to read BMP: %w", err)
	}

	var buf bytes.Buffer
	if err := packToWriter(&buf, pixelData, bmi, original); err != nil {
		return nil, err
	}

	return buf.Bytes(), nil
}

// writeSector writes data as an uncompressed sector.
func writeSector(w io.Writer, data []byte) error {
	hdr := &SectorHeader{
		OriginalLength:  uint32(len(data)),
		OriginalLength2: uint32(len(data)),
		Length:          uint32(len(data)),
	}

	if err := WriteSectorHeader(w, hdr); err != nil {
		return err
	}

	_, err := w.Write(data)
	return err
}

// encodeColorMapWithAlpha separates RGBA pixel data into RGB and Alpha.
func encodeColorMapWithAlpha(decodedData []byte, bmi *BitmapInfoHeader, original *UnpackResult) ([]byte, []byte) {
	width := int(original.InfoHeader.Width)
	height := int(original.InfoHeader.Height)

	// RGB stride must be padded to 4 bytes
	rgbStride := (width*int(original.InfoHeader.BitCount)/8 + 3) &^ 3

	var alphaSize int
	if original.InfoHeader.BitCount == 8 {
		alphaSize = height * width
	} else {
		alphaSize = len(original.AlphaData)
	}

	var encodedSize int
	if original.InfoHeader.BitCount == 8 {
		encodedSize = height * rgbStride
	} else {
		encodedSize = len(original.PixelData)
	}

	alphaData := make([]byte, alphaSize)
	encodedData := make([]byte, encodedSize)

	// Build palette lookup if needed
	var palList []RGBQuad
	var additionalPalMap map[RGBQuad]int
	if original.InfoHeader.BitCount == 8 && original.Palette != nil {
		palList = original.Palette
		additionalPalMap = make(map[RGBQuad]int)
	}

	for y := 0; y < height; y++ {
		// Alpha Y is inverted
		alphaLineIndex := (height - y - 1) * width
		rgbaLineIndex := y * int(bmi.Width) * 4
		rgbLineIndex := y * rgbStride

		for x := 0; x < width; x++ {
			blueIndex := rgbaLineIndex + x*4

			if original.InfoHeader.BitCount == 8 {
				// Find nearest palette color
				newPal := RGBQuad{
					Blue:  decodedData[blueIndex],
					Green: decodedData[blueIndex+1],
					Red:   decodedData[blueIndex+2],
				}
				palIndex := findNearestPalette(newPal, palList, additionalPalMap)
				encodedData[y*rgbStride+x] = byte(palIndex)
			} else {
				// 24-bit RGB
				encodedData[rgbLineIndex+x*3] = decodedData[blueIndex]
				encodedData[rgbLineIndex+x*3+1] = decodedData[blueIndex+1]
				encodedData[rgbLineIndex+x*3+2] = decodedData[blueIndex+2]
			}
			alphaData[alphaLineIndex+x] = decodedData[blueIndex+3]
		}
	}

	return encodedData, alphaData
}

// findNearestPalette finds the nearest color in the palette.
func findNearestPalette(input RGBQuad, palette []RGBQuad, cache map[RGBQuad]int) int {
	// Check cache first
	if idx, ok := cache[input]; ok {
		return idx
	}

	// Check for exact match
	for i, c := range palette {
		if c.Blue == input.Blue && c.Green == input.Green && c.Red == input.Red {
			return i
		}
	}

	// Find nearest by Euclidean distance
	minDist := math.MaxFloat64
	minIdx := 0
	for i, c := range palette {
		dist := math.Sqrt(
			math.Pow(float64(c.Blue)-float64(input.Blue), 2) +
				math.Pow(float64(c.Green)-float64(input.Green), 2) +
				math.Pow(float64(c.Red)-float64(input.Red), 2))
		if dist < minDist {
			minDist = dist
			minIdx = i
		}
	}

	cache[input] = minIdx
	return minIdx
}
