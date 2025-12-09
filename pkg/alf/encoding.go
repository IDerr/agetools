package alf

import (
	"bytes"
	"encoding/binary"
	"unicode/utf16"
)

// DecodeUTF16LE decodes a UTF-16LE byte slice to a Go string.
// It stops at the first null terminator (0x0000).
func DecodeUTF16LE(data []byte) string {
	if len(data) < 2 {
		return ""
	}

	// Convert bytes to uint16 slice
	u16 := make([]uint16, len(data)/2)
	for i := 0; i < len(u16); i++ {
		u16[i] = binary.LittleEndian.Uint16(data[i*2:])
	}

	// Find null terminator
	end := len(u16)
	for i, c := range u16 {
		if c == 0 {
			end = i
			break
		}
	}

	return string(utf16.Decode(u16[:end]))
}

// FindUTF16Null finds the position of a UTF-16LE null terminator (0x0000).
// Returns the byte offset of the null terminator, or -1 if not found.
func FindUTF16Null(data []byte, start int) int {
	for i := start; i+1 < len(data); i += 2 {
		if data[i] == 0 && data[i+1] == 0 {
			return i
		}
	}
	return -1
}

// ReadUTF16String reads a null-terminated UTF-16LE string from data starting at offset.
// Returns the decoded string and the number of bytes consumed (including null terminator).
func ReadUTF16String(data []byte, offset int) (string, int) {
	nullPos := FindUTF16Null(data, offset)
	if nullPos == -1 {
		// No null found, read to end
		return DecodeUTF16LE(data[offset:]), len(data) - offset
	}

	str := DecodeUTF16LE(data[offset:nullPos])
	// +2 to include the null terminator
	return str, nullPos - offset + 2
}

// ReadUTF16StringPadded reads a UTF-16LE string from a fixed-size field.
// The string is null-terminated within the field.
func ReadUTF16StringPadded(data []byte, offset, size int) string {
	if offset+size > len(data) {
		size = len(data) - offset
	}
	return DecodeUTF16LE(data[offset : offset+size])
}

// EncodeUTF16LE encodes a Go string to UTF-16LE bytes.
func EncodeUTF16LE(s string) []byte {
	runes := []rune(s)
	u16 := utf16.Encode(runes)

	buf := new(bytes.Buffer)
	for _, c := range u16 {
		binary.Write(buf, binary.LittleEndian, c)
	}
	return buf.Bytes()
}
