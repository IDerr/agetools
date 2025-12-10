package bin

import (
	"encoding/binary"
	"fmt"
	"math"
	"sort"
	"strings"

	"golang.org/x/text/encoding/japanese"
	"golang.org/x/text/transform"
)

// Disassemble parses a BIN file and returns a Script structure
func Disassemble(data []byte) (*Script, error) {
	header, err := ReadHeader(data)
	if err != nil {
		return nil, fmt.Errorf("failed to read header: %w", err)
	}

	script := &Script{
		Header:  *header,
		Labels:  make(map[int]string),
		RawData: data,
	}

	// Calculate where instruction data ends
	dataEnd := header.DataArrayEnd()
	if dataEnd == 0 || dataEnd > len(data) {
		// Try to find end by parsing until we hit string data
		dataEnd = len(data)
	}

	// First pass: parse all instructions
	offset := header.GetLength()
	for offset < dataEnd {
		instr, err := parseInstruction(data, offset, header)
		if err != nil {
			// If we hit an error, we might have reached footer data
			break
		}
		script.Instructions = append(script.Instructions, instr)
		offset += instr.Size()
	}

	// Build instruction offset map first
	instrOffsets := make(map[int]bool)
	for i := range script.Instructions {
		instrOffsets[script.Instructions[i].Offset] = true
	}

	// Second pass: identify labels from control flow instructions
	labelOffsets := make(map[int]bool)
	for i := range script.Instructions {
		instr := &script.Instructions[i]
		if IsControlFlow(instr.Opcode) {
			for j := range instr.Arguments {
				if IsLabelArgument(instr, j) {
					// Calculate target offset
					targetOffset := header.GetLength() + int(instr.Arguments[j].RawValue)*4

					// Only create label if target offset exists in code
					if instrOffsets[targetOffset] {
						labelOffsets[targetOffset] = true
						instr.Arguments[j].IsLabel = true
						instr.Arguments[j].LabelName = fmt.Sprintf("label_%08X", targetOffset)
					}
					// Otherwise, leave as raw value (external function address)
				}
			}
		}
	}

	// Create label map
	for off := range labelOffsets {
		script.Labels[off] = fmt.Sprintf("label_%08X", off)
	}

	// Third pass: decode strings for string arguments
	for i := range script.Instructions {
		instr := &script.Instructions[i]
		for j := range instr.Arguments {
			arg := &instr.Arguments[j]
			if arg.Type == ArgString {
				strOffset := header.GetLength() + int(arg.RawValue)*4
				str, err := decodeString(data, strOffset, header.Version)
				if err == nil {
					arg.StringVal = str
					script.Strings = append(script.Strings, str)
				}
			}
		}

		// Handle copy-local-array (0x64) - second argument is array reference
		if instr.Opcode == 0x64 && len(instr.Arguments) >= 2 {
			arg := &instr.Arguments[1]
			if arg.Type == ArgString || arg.Type == ArgImmediate {
				arrayOffset := header.GetLength() + int(arg.RawValue)*4
				arr, err := readDataArray(data, arrayOffset)
				if err == nil {
					arg.DataArray = arr
				}
			}
		}
	}

	// Read footer tables
	script.Tables[0] = readTable(data, header.GetLength()+int(header.Table1Offset)*4, int(header.Table1Length))
	script.Tables[1] = readTable(data, header.GetLength()+int(header.Table2Offset)*4, int(header.Table2Length))
	script.Tables[2] = readTable(data, header.GetLength()+int(header.Table3Offset)*4, int(header.Table3Length))

	return script, nil
}

// parseInstruction parses a single instruction from the data
func parseInstruction(data []byte, offset int, header *Header) (Instruction, error) {
	if offset+4 > len(data) {
		return Instruction{}, ErrUnexpectedEOF
	}

	opcode := binary.LittleEndian.Uint32(data[offset:])
	def := LookupOpcode(opcode)
	if def == nil {
		return Instruction{}, fmt.Errorf("%w: 0x%X at offset 0x%X", ErrUnknownOpcode, opcode, offset)
	}

	instr := Instruction{
		Offset:     offset,
		Opcode:     opcode,
		Definition: def,
		Arguments:  make([]Argument, def.ArgCount),
	}

	argOffset := offset + 4
	for i := 0; i < def.ArgCount; i++ {
		if argOffset+8 > len(data) {
			return Instruction{}, ErrUnexpectedEOF
		}
		instr.Arguments[i] = Argument{
			Type:     ArgumentType(binary.LittleEndian.Uint32(data[argOffset:])),
			RawValue: binary.LittleEndian.Uint32(data[argOffset+4:]),
		}
		argOffset += 8
	}

	return instr, nil
}

// decodeString decodes a XOR'd string from the data
func decodeString(data []byte, offset int, version FormatVersion) (string, error) {
	if offset >= len(data) {
		return "", ErrUnexpectedEOF
	}

	if version == FormatSYS5 {
		// UTF-16LE XOR'd with 0xFFFF
		var runes []rune
		for i := offset; i+1 < len(data); i += 2 {
			char := binary.LittleEndian.Uint16(data[i:])
			if char == 0xFFFF {
				break
			}
			decoded := char ^ 0xFFFF
			runes = append(runes, rune(decoded))
		}
		return string(runes), nil
	}

	// SYS4: Shift-JIS XOR'd with 0xFF
	var sjisBytes []byte
	for i := offset; i < len(data); i++ {
		char := data[i]
		if char == 0xFF {
			break
		}
		sjisBytes = append(sjisBytes, char^0xFF)
	}

	// Convert Shift-JIS to UTF-8
	decoder := japanese.ShiftJIS.NewDecoder()
	utf8Bytes, _, err := transform.Bytes(decoder, sjisBytes)
	if err != nil {
		return string(sjisBytes), nil // Return raw bytes if conversion fails
	}
	return string(utf8Bytes), nil
}

// readDataArray reads a data array from the footer
func readDataArray(data []byte, offset int) ([]uint32, error) {
	if offset+4 > len(data) {
		return nil, ErrUnexpectedEOF
	}

	length := binary.LittleEndian.Uint32(data[offset:])
	if offset+4+int(length)*4 > len(data) {
		return nil, ErrUnexpectedEOF
	}

	arr := make([]uint32, length)
	for i := uint32(0); i < length; i++ {
		arr[i] = binary.LittleEndian.Uint32(data[offset+4+int(i)*4:])
	}
	return arr, nil
}

// readTable reads a table of uint32 values
func readTable(data []byte, offset int, length int) []uint32 {
	if offset < 0 || length <= 0 || offset+length*4 > len(data) {
		return nil
	}
	table := make([]uint32, length)
	for i := 0; i < length; i++ {
		table[i] = binary.LittleEndian.Uint32(data[offset+i*4:])
	}
	return table
}

// ToText converts a Script to human-readable assembly text
func (s *Script) ToText() string {
	var sb strings.Builder

	// Write header info
	sb.WriteString("==Binary Information - do not edit==\n")
	sb.WriteString(fmt.Sprintf("signature = %s\n", strings.TrimRight(s.Header.Signature, "\x00 ")))
	sb.WriteString(fmt.Sprintf("local_vars = { %d %d %d %d %d %d }\n",
		s.Header.LocalInteger1, s.Header.LocalFloats, s.Header.LocalStrings1,
		s.Header.LocalInteger2, s.Header.UnknownData, s.Header.LocalStrings2))
	sb.WriteString("====\n\n")

	// Get sorted label offsets for output
	sortedOffsets := make([]int, 0, len(s.Instructions))
	for _, instr := range s.Instructions {
		sortedOffsets = append(sortedOffsets, instr.Offset)
	}
	sort.Ints(sortedOffsets)

	// Write instructions
	for _, instr := range s.Instructions {
		// Check if this offset has a label
		if label, ok := s.Labels[instr.Offset]; ok {
			sb.WriteString(fmt.Sprintf("\n%s:\n", label))
		}

		// Write instruction
		sb.WriteString(fmt.Sprintf("    %s", instr.Definition.Label))

		// Write arguments
		for i, arg := range instr.Arguments {
			if i > 0 || len(instr.Arguments) > 0 {
				sb.WriteString(" ")
			}
			sb.WriteString(formatArgument(&arg, &instr, i))
		}
		sb.WriteString("\n")
	}

	return sb.String()
}

// formatArgument formats an argument for text output
func formatArgument(arg *Argument, instr *Instruction, argIdx int) string {
	// Label reference
	if arg.IsLabel {
		return arg.LabelName
	}

	// String value
	if arg.Type == ArgString && arg.StringVal != "" {
		// Escape special characters
		escaped := strings.ReplaceAll(arg.StringVal, "\\", "\\\\")
		escaped = strings.ReplaceAll(escaped, "\"", "\\\"")
		escaped = strings.ReplaceAll(escaped, "\n", "\\n")
		escaped = strings.ReplaceAll(escaped, "\r", "\\r")
		escaped = strings.ReplaceAll(escaped, "\t", "\\t")
		return fmt.Sprintf("\"%s\"", escaped)
	}

	// Data array
	if len(arg.DataArray) > 0 {
		var parts []string
		for _, v := range arg.DataArray {
			parts = append(parts, fmt.Sprintf("%d", v))
		}
		return fmt.Sprintf("[%s]", strings.Join(parts, ", "))
	}

	// Variable reference with type prefix
	typeStr := arg.Type.String()
	if typeStr != "" {
		return fmt.Sprintf("%s:%d", typeStr, arg.RawValue)
	}

	// Float value
	if arg.Type == ArgFloat {
		bits := arg.RawValue
		f := math.Float32frombits(bits)
		return fmt.Sprintf("%g", f)
	}

	// Immediate value
	return fmt.Sprintf("%d", arg.RawValue)
}

// DisassembleToText is a convenience function that disassembles and returns text
func DisassembleToText(data []byte) (string, error) {
	script, err := Disassemble(data)
	if err != nil {
		return "", err
	}
	return script.ToText(), nil
}
