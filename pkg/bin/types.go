// Package bin handles Eushully AGE engine BIN script disassembly and assembly.
package bin

import (
	"encoding/binary"
	"errors"
	"io"
)

// Format version constants
type FormatVersion int

const (
	FormatSYS4 FormatVersion = 4 // SYS4xxxx - Shift-JIS strings
	FormatSYS5 FormatVersion = 5 // SYS5501 - UTF-16LE strings
)

// Header size constants
const (
	SYS4HeaderSize = 0x3C // 60 bytes
	SYS5HeaderSize = 0x44 // 68 bytes
)

// Common errors
var (
	ErrInvalidMagic     = errors.New("invalid BIN file magic")
	ErrUnknownOpcode    = errors.New("unknown opcode")
	ErrInvalidArgType   = errors.New("invalid argument type")
	ErrInvalidLabel     = errors.New("invalid label reference")
	ErrUnexpectedEOF    = errors.New("unexpected end of file")
	ErrInvalidFormat    = errors.New("invalid file format")
	ErrLabelNotFound    = errors.New("label not found")
	ErrDuplicateLabel   = errors.New("duplicate label")
	ErrInstructionParse = errors.New("instruction parse error")
)

// ArgumentType represents the type of an instruction argument
type ArgumentType uint32

const (
	ArgImmediate       ArgumentType = 0x00 // Literal integer value
	ArgFloat           ArgumentType = 0x01 // Float literal
	ArgString          ArgumentType = 0x02 // String reference (XOR'd data)
	ArgGlobalInt       ArgumentType = 0x03 // Global integer variable
	ArgGlobalFloat     ArgumentType = 0x04 // Global float variable
	ArgGlobalString    ArgumentType = 0x05 // Global string variable
	ArgGlobalPtr       ArgumentType = 0x06 // Global pointer
	ArgGlobalStringPtr ArgumentType = 0x08 // Global string pointer
	ArgLocalInt        ArgumentType = 0x09 // Local integer variable
	ArgLocalFloat      ArgumentType = 0x0A // Local float variable
	ArgLocalString     ArgumentType = 0x0B // Local string variable
	ArgLocalPtr        ArgumentType = 0x0C // Local pointer
	ArgLocalFloatPtr   ArgumentType = 0x0D // Local float pointer
	ArgLocalStringPtr  ArgumentType = 0x0E // Local string pointer
	// Extended types for newer games
	ArgExtended8003 ArgumentType = 0x8003
	ArgExtended8005 ArgumentType = 0x8005
	ArgExtended8009 ArgumentType = 0x8009
	ArgExtended800B ArgumentType = 0x800B
)

// String returns the type label for display
func (t ArgumentType) String() string {
	switch t {
	case ArgImmediate:
		return ""
	case ArgFloat:
		return "float"
	case ArgString:
		return "string"
	case ArgGlobalInt:
		return "global-int"
	case ArgGlobalFloat:
		return "global-float"
	case ArgGlobalString:
		return "global-string"
	case ArgGlobalPtr:
		return "global-ptr"
	case ArgGlobalStringPtr:
		return "global-string-ptr"
	case ArgLocalInt:
		return "local-int"
	case ArgLocalFloat:
		return "local-float"
	case ArgLocalString:
		return "local-string"
	case ArgLocalPtr:
		return "local-ptr"
	case ArgLocalFloatPtr:
		return "local-float-ptr"
	case ArgLocalStringPtr:
		return "local-string-ptr"
	case ArgExtended8003:
		return "ext-8003"
	case ArgExtended8005:
		return "ext-8005"
	case ArgExtended8009:
		return "ext-8009"
	case ArgExtended800B:
		return "ext-800B"
	default:
		return "unknown"
	}
}

// IsVariable returns true if this argument type represents a variable
func (t ArgumentType) IsVariable() bool {
	switch t {
	case ArgGlobalInt, ArgGlobalFloat, ArgGlobalString, ArgGlobalPtr, ArgGlobalStringPtr,
		ArgLocalInt, ArgLocalFloat, ArgLocalString, ArgLocalPtr, ArgLocalFloatPtr, ArgLocalStringPtr,
		ArgExtended8003, ArgExtended8005, ArgExtended8009, ArgExtended800B:
		return true
	default:
		return false
	}
}

// Header represents the BIN file header
type Header struct {
	Version        FormatVersion
	Signature      string // "SYS4xxxx" or "SYS5501 "
	LocalInteger1  uint32 // local_integer_1
	LocalFloats    uint32 // local_floats
	LocalStrings1  uint32 // local_strings_1
	LocalInteger2  uint32 // local_integer_2
	UnknownData    uint32 // unknown_data
	LocalStrings2  uint32 // local_strings_2
	SubHeaderLen   uint32 // sub_header_length (always 0x1C)
	Table1Length   uint32 // table_1_length (opcode 0x71 offsets)
	Table1Offset   uint32 // table_1_offset (in 4-byte units from header end)
	Table2Length   uint32 // table_2_length (opcode 0x03 offsets)
	Table2Offset   uint32 // table_2_offset
	Table3Length   uint32 // table_3_length (opcode 0x8F offsets)
	Table3Offset   uint32 // table_3_offset
}

// GetLength returns the header length in bytes
func (h *Header) GetLength() int {
	if h.Version == FormatSYS5 {
		return SYS5HeaderSize
	}
	return SYS4HeaderSize
}

// DataArrayEnd returns the byte offset where instruction data ends
// This is calculated from table offsets
func (h *Header) DataArrayEnd() int {
	// The first table offset indicates where data ends
	if h.Table1Length > 0 {
		return h.GetLength() + int(h.Table1Offset)*4
	}
	if h.Table2Length > 0 {
		return h.GetLength() + int(h.Table2Offset)*4
	}
	if h.Table3Length > 0 {
		return h.GetLength() + int(h.Table3Offset)*4
	}
	return 0
}

// Argument represents an instruction argument
type Argument struct {
	Type       ArgumentType
	RawValue   uint32
	StringVal  string    // Decoded string (if type is ArgString)
	DataArray  []uint32  // Array data (if opcode is 0x64)
	IsLabel    bool      // True if this argument is a code label reference
	LabelName  string    // Label name for display (e.g., "label_00001234")
}

// Instruction represents a single BIN instruction
type Instruction struct {
	Offset     int                    // Byte offset in file
	Opcode     uint32                 // Opcode value
	Definition *InstructionDefinition // Opcode definition
	Arguments  []Argument             // Instruction arguments
}

// Size returns the instruction size in bytes
func (i *Instruction) Size() int {
	return 4 + len(i.Arguments)*8 // 4 bytes opcode + 8 bytes per argument
}

// Script represents a complete disassembled BIN script
type Script struct {
	Header       Header
	Instructions []Instruction
	Labels       map[int]string // Offset -> label name mapping
	Strings      []string       // All decoded strings
	Tables       [3][]uint32    // The three offset tables
	RawData      []byte         // Original file data for reference
}

// DetectFormat detects the format version from raw file data
func DetectFormat(data []byte) (FormatVersion, error) {
	if len(data) < 16 {
		return 0, io.ErrUnexpectedEOF
	}

	// Try SYS5 first (UTF-16LE): check if bytes 1,3,5,7 are 0x00
	if data[1] == 0 && data[3] == 0 && data[5] == 0 && data[7] == 0 {
		// Read as UTF-16LE
		if data[0] == 'S' && data[2] == 'Y' && data[4] == 'S' && data[6] == '5' {
			return FormatSYS5, nil
		}
	}

	// Try SYS4 (ASCII/UTF-8)
	if data[0] == 'S' && data[1] == 'Y' && data[2] == 'S' && data[3] == '4' {
		return FormatSYS4, nil
	}

	return 0, ErrInvalidMagic
}

// ReadHeader reads and parses the BIN file header
func ReadHeader(data []byte) (*Header, error) {
	version, err := DetectFormat(data)
	if err != nil {
		return nil, err
	}

	headerSize := SYS4HeaderSize
	if version == FormatSYS5 {
		headerSize = SYS5HeaderSize
	}

	if len(data) < headerSize {
		return nil, io.ErrUnexpectedEOF
	}

	h := &Header{Version: version}

	// Read signature
	if version == FormatSYS5 {
		// UTF-16LE signature (16 bytes)
		h.Signature = decodeUTF16LE(data[:16])
		// Rest of header starts at offset 0x10
		offset := 0x10
		h.LocalInteger1 = binary.LittleEndian.Uint32(data[offset:])
		h.LocalFloats = binary.LittleEndian.Uint32(data[offset+4:])
		h.LocalStrings1 = binary.LittleEndian.Uint32(data[offset+8:])
		h.LocalInteger2 = binary.LittleEndian.Uint32(data[offset+12:])
		h.UnknownData = binary.LittleEndian.Uint32(data[offset+16:])
		h.LocalStrings2 = binary.LittleEndian.Uint32(data[offset+20:])
		h.SubHeaderLen = binary.LittleEndian.Uint32(data[offset+24:])
		h.Table1Length = binary.LittleEndian.Uint32(data[offset+28:])
		h.Table1Offset = binary.LittleEndian.Uint32(data[offset+32:])
		h.Table2Length = binary.LittleEndian.Uint32(data[offset+36:])
		h.Table2Offset = binary.LittleEndian.Uint32(data[offset+40:])
		h.Table3Length = binary.LittleEndian.Uint32(data[offset+44:])
		h.Table3Offset = binary.LittleEndian.Uint32(data[offset+48:])
	} else {
		// ASCII signature (8 bytes)
		h.Signature = string(data[:8])
		offset := 0x08
		h.LocalInteger1 = binary.LittleEndian.Uint32(data[offset:])
		h.LocalFloats = binary.LittleEndian.Uint32(data[offset+4:])
		h.LocalStrings1 = binary.LittleEndian.Uint32(data[offset+8:])
		h.LocalInteger2 = binary.LittleEndian.Uint32(data[offset+12:])
		h.UnknownData = binary.LittleEndian.Uint32(data[offset+16:])
		h.LocalStrings2 = binary.LittleEndian.Uint32(data[offset+20:])
		h.SubHeaderLen = binary.LittleEndian.Uint32(data[offset+24:])
		h.Table1Length = binary.LittleEndian.Uint32(data[offset+28:])
		h.Table1Offset = binary.LittleEndian.Uint32(data[offset+32:])
		h.Table2Length = binary.LittleEndian.Uint32(data[offset+36:])
		h.Table2Offset = binary.LittleEndian.Uint32(data[offset+40:])
		h.Table3Length = binary.LittleEndian.Uint32(data[offset+44:])
		h.Table3Offset = binary.LittleEndian.Uint32(data[offset+48:])
	}

	return h, nil
}

// WriteHeader serializes the header to bytes
func (h *Header) WriteHeader() []byte {
	var buf []byte

	if h.Version == FormatSYS5 {
		buf = make([]byte, SYS5HeaderSize)
		// Write UTF-16LE signature (pad with spaces to 8 characters)
		sigStr := h.Signature
		for len(sigStr) < 8 {
			sigStr += " "
		}
		sig := encodeUTF16LE(sigStr[:8])
		copy(buf[:16], sig)
		offset := 0x10
		binary.LittleEndian.PutUint32(buf[offset:], h.LocalInteger1)
		binary.LittleEndian.PutUint32(buf[offset+4:], h.LocalFloats)
		binary.LittleEndian.PutUint32(buf[offset+8:], h.LocalStrings1)
		binary.LittleEndian.PutUint32(buf[offset+12:], h.LocalInteger2)
		binary.LittleEndian.PutUint32(buf[offset+16:], h.UnknownData)
		binary.LittleEndian.PutUint32(buf[offset+20:], h.LocalStrings2)
		binary.LittleEndian.PutUint32(buf[offset+24:], h.SubHeaderLen)
		binary.LittleEndian.PutUint32(buf[offset+28:], h.Table1Length)
		binary.LittleEndian.PutUint32(buf[offset+32:], h.Table1Offset)
		binary.LittleEndian.PutUint32(buf[offset+36:], h.Table2Length)
		binary.LittleEndian.PutUint32(buf[offset+40:], h.Table2Offset)
		binary.LittleEndian.PutUint32(buf[offset+44:], h.Table3Length)
		binary.LittleEndian.PutUint32(buf[offset+48:], h.Table3Offset)
	} else {
		buf = make([]byte, SYS4HeaderSize)
		copy(buf[:8], h.Signature)
		offset := 0x08
		binary.LittleEndian.PutUint32(buf[offset:], h.LocalInteger1)
		binary.LittleEndian.PutUint32(buf[offset+4:], h.LocalFloats)
		binary.LittleEndian.PutUint32(buf[offset+8:], h.LocalStrings1)
		binary.LittleEndian.PutUint32(buf[offset+12:], h.LocalInteger2)
		binary.LittleEndian.PutUint32(buf[offset+16:], h.UnknownData)
		binary.LittleEndian.PutUint32(buf[offset+20:], h.LocalStrings2)
		binary.LittleEndian.PutUint32(buf[offset+24:], h.SubHeaderLen)
		binary.LittleEndian.PutUint32(buf[offset+28:], h.Table1Length)
		binary.LittleEndian.PutUint32(buf[offset+32:], h.Table1Offset)
		binary.LittleEndian.PutUint32(buf[offset+36:], h.Table2Length)
		binary.LittleEndian.PutUint32(buf[offset+40:], h.Table2Offset)
		binary.LittleEndian.PutUint32(buf[offset+44:], h.Table3Length)
		binary.LittleEndian.PutUint32(buf[offset+48:], h.Table3Offset)
	}

	return buf
}

// decodeUTF16LE decodes UTF-16LE bytes to string
func decodeUTF16LE(data []byte) string {
	if len(data) < 2 {
		return ""
	}
	runes := make([]rune, 0, len(data)/2)
	for i := 0; i+1 < len(data); i += 2 {
		r := rune(binary.LittleEndian.Uint16(data[i:]))
		if r == 0 {
			break
		}
		runes = append(runes, r)
	}
	return string(runes)
}

// encodeUTF16LE encodes a string to UTF-16LE bytes
func encodeUTF16LE(s string) []byte {
	runes := []rune(s)
	buf := make([]byte, len(runes)*2)
	for i, r := range runes {
		binary.LittleEndian.PutUint16(buf[i*2:], uint16(r))
	}
	return buf
}
