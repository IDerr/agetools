package bin

import (
	"bufio"
	"encoding/binary"
	"fmt"
	"math"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"golang.org/x/text/encoding/japanese"
	"golang.org/x/text/transform"
)

// AssembleResult contains the assembled binary and metadata
type AssembleResult struct {
	Data   []byte
	Header Header
}

// Assemble parses assembly text and produces a BIN file
func Assemble(text string, version FormatVersion) (*AssembleResult, error) {
	parser := &assemblyParser{
		version:       version,
		labels:        make(map[string]int),
		labelRefs:     make([]labelReference, 0),
		instructions:  make([]parsedInstruction, 0),
		strings:       make([]string, 0),
		stringOffsets: make(map[string]int),
		arrays:        make([][]uint32, 0),
		arrayOffsets:  make(map[int]int), // instruction index -> array offset
		table1Offsets: make([]uint32, 0), // opcode 0x71
		table2Offsets: make([]uint32, 0), // opcode 0x03
		table3Offsets: make([]uint32, 0), // opcode 0x8F
	}

	// Parse header
	if err := parser.parseHeader(text); err != nil {
		return nil, err
	}

	// Parse instructions
	if err := parser.parseInstructions(text); err != nil {
		return nil, err
	}

	// Build binary
	return parser.build()
}

// AssembleFromScript rebuilds a BIN file from a Script structure
func AssembleFromScript(script *Script) (*AssembleResult, error) {
	return Assemble(script.ToText(), script.Header.Version)
}

type labelReference struct {
	instrIndex int
	argIndex   int
	labelName  string
}

type parsedInstruction struct {
	opcode    uint32
	def       *InstructionDefinition
	arguments []parsedArgument
	offset    int // calculated offset
}

type parsedArgument struct {
	argType   ArgumentType
	rawValue  uint32
	stringVal string
	arrayVal  []uint32
	isLabel   bool
	labelName string
}

type assemblyParser struct {
	version       FormatVersion
	header        Header
	labels        map[string]int // label name -> instruction index
	labelRefs     []labelReference
	instructions  []parsedInstruction
	strings       []string
	stringOffsets map[string]int
	arrays        [][]uint32
	arrayOffsets  map[int]int
	table1Offsets []uint32
	table2Offsets []uint32
	table3Offsets []uint32
}

var (
	headerLineRE  = regexp.MustCompile(`^(\w+)\s*=\s*(.+)$`)
	labelRE       = regexp.MustCompile(`^(label_[0-9A-Fa-f]+):$`)
	instructionRE = regexp.MustCompile(`^\s*(\S+)(.*)$`)
	stringArgRE   = regexp.MustCompile(`^"((?:[^"\\]|\\.)*)"`)
	arrayArgRE    = regexp.MustCompile(`^\[([^\]]*)\]`)
	typedArgRE    = regexp.MustCompile(`^(\w+(?:-\w+)*):(-?\d+)$`)
	labelArgRE    = regexp.MustCompile(`^label_([0-9A-Fa-f]+)$`)
)

func (p *assemblyParser) parseHeader(text string) error {
	scanner := bufio.NewScanner(strings.NewReader(text))
	inHeader := false

	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())

		if line == "==Binary Information - do not edit==" {
			inHeader = true
			continue
		}
		if line == "====" {
			break
		}

		if !inHeader {
			continue
		}

		matches := headerLineRE.FindStringSubmatch(line)
		if matches == nil {
			continue
		}

		key, value := matches[1], matches[2]
		switch key {
		case "signature":
			p.header.Signature = value
			// Detect version from signature
			if strings.HasPrefix(value, "SYS5") {
				p.version = FormatSYS5
				p.header.Version = FormatSYS5
			} else if strings.HasPrefix(value, "SYS4") {
				p.version = FormatSYS4
				p.header.Version = FormatSYS4
			}
		case "local_vars":
			// Parse { a b c d e f }
			value = strings.Trim(value, "{ }")
			parts := strings.Fields(value)
			if len(parts) >= 6 {
				p.header.LocalInteger1 = parseUint32(parts[0])
				p.header.LocalFloats = parseUint32(parts[1])
				p.header.LocalStrings1 = parseUint32(parts[2])
				p.header.LocalInteger2 = parseUint32(parts[3])
				p.header.UnknownData = parseUint32(parts[4])
				p.header.LocalStrings2 = parseUint32(parts[5])
			}
		}
	}

	p.header.SubHeaderLen = 0x1C // Always 0x1C
	return scanner.Err()
}

func (p *assemblyParser) parseInstructions(text string) error {
	scanner := bufio.NewScanner(strings.NewReader(text))
	pastHeader := false

	for scanner.Scan() {
		line := scanner.Text()
		trimmed := strings.TrimSpace(line)

		// Skip until we're past the header
		if trimmed == "====" {
			pastHeader = true
			continue
		}
		if !pastHeader {
			continue
		}

		// Skip empty lines and comments
		if trimmed == "" || strings.HasPrefix(trimmed, "//") || strings.HasPrefix(trimmed, "#") {
			continue
		}

		// Check for label
		if matches := labelRE.FindStringSubmatch(trimmed); matches != nil {
			labelName := matches[1]
			p.labels[labelName] = len(p.instructions)
			continue
		}

		// Parse instruction
		matches := instructionRE.FindStringSubmatch(trimmed)
		if matches == nil {
			continue
		}

		mnemonic := matches[1]
		argsStr := strings.TrimSpace(matches[2])

		def := LookupLabel(mnemonic)
		if def == nil {
			return fmt.Errorf("%w: %s", ErrUnknownOpcode, mnemonic)
		}

		instr := parsedInstruction{
			opcode:    def.Opcode,
			def:       def,
			arguments: make([]parsedArgument, 0, def.ArgCount),
		}

		// Parse arguments
		if err := p.parseArguments(&instr, argsStr); err != nil {
			return fmt.Errorf("error parsing arguments for %s: %w", mnemonic, err)
		}

		// Track special opcodes for tables
		instrIndex := len(p.instructions)
		switch def.Opcode {
		case 0x71:
			p.table1Offsets = append(p.table1Offsets, uint32(instrIndex))
		case 0x03:
			p.table2Offsets = append(p.table2Offsets, uint32(instrIndex))
		case 0x8F:
			p.table3Offsets = append(p.table3Offsets, uint32(instrIndex))
		}

		p.instructions = append(p.instructions, instr)
	}

	return scanner.Err()
}

func (p *assemblyParser) parseArguments(instr *parsedInstruction, argsStr string) error {
	argsStr = strings.TrimSpace(argsStr)
	if argsStr == "" {
		return nil
	}

	for len(argsStr) > 0 && len(instr.arguments) < instr.def.ArgCount {
		argsStr = strings.TrimSpace(argsStr)
		if argsStr == "" {
			break
		}

		var arg parsedArgument

		// Try string argument
		if strings.HasPrefix(argsStr, "\"") {
			matches := stringArgRE.FindStringSubmatch(argsStr)
			if matches != nil {
				arg.argType = ArgString
				arg.stringVal = unescapeString(matches[1])
				argsStr = strings.TrimPrefix(argsStr, matches[0])
				instr.arguments = append(instr.arguments, arg)
				continue
			}
		}

		// Try array argument
		if strings.HasPrefix(argsStr, "[") {
			matches := arrayArgRE.FindStringSubmatch(argsStr)
			if matches != nil {
				arg.argType = ArgImmediate // Will be treated specially
				arg.arrayVal = parseArrayValues(matches[1])
				argsStr = strings.TrimPrefix(argsStr, matches[0])
				instr.arguments = append(instr.arguments, arg)
				continue
			}
		}

		// Find next token
		spaceIdx := strings.IndexAny(argsStr, " \t")
		var token string
		if spaceIdx == -1 {
			token = argsStr
			argsStr = ""
		} else {
			token = argsStr[:spaceIdx]
			argsStr = argsStr[spaceIdx+1:]
		}

		// Try label reference
		if matches := labelArgRE.FindStringSubmatch(token); matches != nil {
			arg.isLabel = true
			arg.labelName = token
			p.labelRefs = append(p.labelRefs, labelReference{
				instrIndex: len(p.instructions),
				argIndex:   len(instr.arguments),
				labelName:  token,
			})
			instr.arguments = append(instr.arguments, arg)
			continue
		}

		// Try typed argument (e.g., local-int:5)
		if matches := typedArgRE.FindStringSubmatch(token); matches != nil {
			arg.argType = parseArgType(matches[1])
			val, _ := strconv.ParseInt(matches[2], 10, 64)
			arg.rawValue = uint32(val)
			instr.arguments = append(instr.arguments, arg)
			continue
		}

		// Try numeric value (immediate or float)
		if val, err := strconv.ParseInt(token, 0, 64); err == nil {
			arg.argType = ArgImmediate
			arg.rawValue = uint32(val)
			instr.arguments = append(instr.arguments, arg)
			continue
		}

		// Try float
		if val, err := strconv.ParseFloat(token, 32); err == nil {
			arg.argType = ArgFloat
			arg.rawValue = math.Float32bits(float32(val))
			instr.arguments = append(instr.arguments, arg)
			continue
		}

		return fmt.Errorf("cannot parse argument: %s", token)
	}

	// Pad with empty arguments if needed
	for len(instr.arguments) < instr.def.ArgCount {
		instr.arguments = append(instr.arguments, parsedArgument{})
	}

	return nil
}

func (p *assemblyParser) build() (*AssembleResult, error) {
	headerLen := p.header.GetLength()

	// Calculate instruction offsets
	offset := headerLen
	for i := range p.instructions {
		p.instructions[i].offset = offset
		offset += 4 + len(p.instructions[i].arguments)*8
	}
	instrEndOffset := offset

	// Build footer data: strings, arrays, tables
	var footerData []byte

	// Encode strings (DO NOT deduplicate - encode each occurrence separately to match original)
	currentStringOffset := instrEndOffset
	for i := range p.instructions {
		for j := range p.instructions[i].arguments {
			arg := &p.instructions[i].arguments[j]
			if arg.argType == ArgString && arg.stringVal != "" {
				// Store offset for this specific argument occurrence
				offsetKey := fmt.Sprintf("%d_%d", i, j)
				p.stringOffsets[offsetKey] = currentStringOffset

				if p.version == FormatSYS5 {
					// UTF-16LE encoding
					runes := []rune(arg.stringVal)
					currentStringOffset += (len(runes) + 1) * 2

					// Write XOR'd string data
					for _, r := range runes {
						encoded := uint16(r) ^ 0xFFFF
						footerData = append(footerData, byte(encoded), byte(encoded>>8))
					}

					// Calculate padding (includes terminator)
					padding := 4 - (currentStringOffset % 4)
					// Write padding + 2 bytes of 0xFF (includes 2-byte terminator)
					for k := 0; k < padding+2; k++ {
						footerData = append(footerData, 0xFF)
					}
					currentStringOffset += padding
				} else {
					// Shift-JIS encoding
					encoder := japanese.ShiftJIS.NewEncoder()
					sjisBytes, _, err := transform.Bytes(encoder, []byte(arg.stringVal))
					if err != nil {
						sjisBytes = []byte(arg.stringVal)
					}

					currentStringOffset += len(sjisBytes) + 1

					// Write XOR'd string data
					for _, b := range sjisBytes {
						footerData = append(footerData, b^0xFF)
					}

					// Calculate padding (includes terminator)
					padding := 4 - (currentStringOffset % 4)
					// Write padding + 1 bytes of 0xFF (includes 1-byte terminator)
					for k := 0; k < padding+1; k++ {
						footerData = append(footerData, 0xFF)
					}
					currentStringOffset += padding
				}
			}
		}
	}

	// Encode arrays
	currentArrayOffset := uint32((currentStringOffset - headerLen) >> 2)
	for i := range p.instructions {
		for j := range p.instructions[i].arguments {
			arg := &p.instructions[i].arguments[j]
			if len(arg.arrayVal) > 0 {
				p.arrayOffsets[i*100+j] = headerLen + int(currentArrayOffset<<2)
				arg.rawValue = currentArrayOffset

				// Write length
				lenBuf := make([]byte, 4)
				binary.LittleEndian.PutUint32(lenBuf, uint32(len(arg.arrayVal)))
				footerData = append(footerData, lenBuf...)
				// Write elements
				for _, v := range arg.arrayVal {
					valBuf := make([]byte, 4)
					binary.LittleEndian.PutUint32(valBuf, v)
					footerData = append(footerData, valBuf...)
				}
				currentArrayOffset += 1 + uint32(len(arg.arrayVal))
			}
		}
	}

	// Calculate table offsets (in 4-byte units from header end)
	table1Start := instrEndOffset + len(footerData)
	for _, idx := range p.table1Offsets {
		instrOffset := p.instructions[idx].offset
		valBuf := make([]byte, 4)
		binary.LittleEndian.PutUint32(valBuf, uint32((instrOffset-headerLen)/4))
		footerData = append(footerData, valBuf...)
	}

	table2Start := instrEndOffset + len(footerData)
	for _, idx := range p.table2Offsets {
		instrOffset := p.instructions[idx].offset
		valBuf := make([]byte, 4)
		binary.LittleEndian.PutUint32(valBuf, uint32((instrOffset-headerLen)/4))
		footerData = append(footerData, valBuf...)
	}

	table3Start := instrEndOffset + len(footerData)
	for _, idx := range p.table3Offsets {
		instrOffset := p.instructions[idx].offset
		valBuf := make([]byte, 4)
		binary.LittleEndian.PutUint32(valBuf, uint32((instrOffset-headerLen)/4))
		footerData = append(footerData, valBuf...)
	}

	// Update header with table info
	p.header.Table1Length = uint32(len(p.table1Offsets))
	p.header.Table1Offset = uint32((table1Start - headerLen) / 4)
	p.header.Table2Length = uint32(len(p.table2Offsets))
	p.header.Table2Offset = uint32((table2Start - headerLen) / 4)
	p.header.Table3Length = uint32(len(p.table3Offsets))
	p.header.Table3Offset = uint32((table3Start - headerLen) / 4)

	// Resolve label references
	for _, ref := range p.labelRefs {
		targetIdx, ok := p.labels[ref.labelName]
		if !ok {
			return nil, fmt.Errorf("%w: %s", ErrLabelNotFound, ref.labelName)
		}
		targetOffset := p.instructions[targetIdx].offset
		p.instructions[ref.instrIndex].arguments[ref.argIndex].rawValue = uint32((targetOffset - headerLen) / 4)
	}

	// Resolve string references
	for i := range p.instructions {
		for j := range p.instructions[i].arguments {
			arg := &p.instructions[i].arguments[j]
			if arg.argType == ArgString && arg.stringVal != "" {
				offsetKey := fmt.Sprintf("%d_%d", i, j)
				strOffset := p.stringOffsets[offsetKey]
				arg.rawValue = uint32((strOffset - headerLen) / 4)
			}
			if len(arg.arrayVal) > 0 {
				arrayOffset := p.arrayOffsets[i*100+j]
				arg.rawValue = uint32((arrayOffset - headerLen) / 4)
			}
		}
	}

	// Build final binary
	totalSize := instrEndOffset + len(footerData)
	data := make([]byte, totalSize)

	// Write header
	headerBytes := p.header.WriteHeader()
	copy(data[:headerLen], headerBytes)

	// Write instructions
	for _, instr := range p.instructions {
		off := instr.offset
		binary.LittleEndian.PutUint32(data[off:], instr.opcode)
		for j, arg := range instr.arguments {
			argOff := off + 4 + j*8
			binary.LittleEndian.PutUint32(data[argOff:], uint32(arg.argType))
			binary.LittleEndian.PutUint32(data[argOff+4:], arg.rawValue)
		}
	}

	// Write footer
	copy(data[instrEndOffset:], footerData)

	return &AssembleResult{
		Data:   data,
		Header: p.header,
	}, nil
}

func (p *assemblyParser) encodeString(s string) []byte {
	if p.version == FormatSYS5 {
		// UTF-16LE XOR'd with 0xFFFF
		runes := []rune(s)
		buf := make([]byte, (len(runes)+1)*2)
		for i, r := range runes {
			encoded := uint16(r) ^ 0xFFFF
			binary.LittleEndian.PutUint16(buf[i*2:], encoded)
		}
		// Terminator
		binary.LittleEndian.PutUint16(buf[len(runes)*2:], 0xFFFF)
		return buf
	}

	// SYS4: Shift-JIS XOR'd with 0xFF
	encoder := japanese.ShiftJIS.NewEncoder()
	sjisBytes, _, err := transform.Bytes(encoder, []byte(s))
	if err != nil {
		sjisBytes = []byte(s)
	}

	buf := make([]byte, len(sjisBytes)+1)
	for i, b := range sjisBytes {
		buf[i] = b ^ 0xFF
	}
	buf[len(sjisBytes)] = 0xFF // Terminator
	return buf
}

func parseUint32(s string) uint32 {
	val, _ := strconv.ParseUint(s, 10, 32)
	return uint32(val)
}

func parseArgType(s string) ArgumentType {
	switch s {
	case "float":
		return ArgFloat
	case "string":
		return ArgString
	case "global-int":
		return ArgGlobalInt
	case "global-float":
		return ArgGlobalFloat
	case "global-string":
		return ArgGlobalString
	case "global-ptr":
		return ArgGlobalPtr
	case "global-string-ptr":
		return ArgGlobalStringPtr
	case "local-int":
		return ArgLocalInt
	case "local-float":
		return ArgLocalFloat
	case "local-string":
		return ArgLocalString
	case "local-ptr":
		return ArgLocalPtr
	case "local-float-ptr":
		return ArgLocalFloatPtr
	case "local-string-ptr":
		return ArgLocalStringPtr
	case "ext-8003":
		return ArgExtended8003
	case "ext-8005":
		return ArgExtended8005
	case "ext-8009":
		return ArgExtended8009
	case "ext-800B":
		return ArgExtended800B
	default:
		return ArgImmediate
	}
}

func parseArrayValues(s string) []uint32 {
	s = strings.TrimSpace(s)
	if s == "" {
		return nil
	}

	parts := strings.Split(s, ",")
	result := make([]uint32, 0, len(parts))
	for _, part := range parts {
		part = strings.TrimSpace(part)
		if val, err := strconv.ParseUint(part, 0, 32); err == nil {
			result = append(result, uint32(val))
		}
	}
	return result
}

func unescapeString(s string) string {
	s = strings.ReplaceAll(s, "\\n", "\n")
	s = strings.ReplaceAll(s, "\\r", "\r")
	s = strings.ReplaceAll(s, "\\t", "\t")
	s = strings.ReplaceAll(s, "\\\"", "\"")
	s = strings.ReplaceAll(s, "\\\\", "\\")
	return s
}

// VerifyRoundTrip disassembles and reassembles a BIN file, returning true if they match
func VerifyRoundTrip(originalData []byte) (bool, error) {
	// Disassemble
	script, err := Disassemble(originalData)
	if err != nil {
		return false, fmt.Errorf("disassembly failed: %w", err)
	}

	// Reassemble
	result, err := Assemble(script.ToText(), script.Header.Version)
	if err != nil {
		return false, fmt.Errorf("assembly failed: %w", err)
	}

	// Compare
	if len(originalData) != len(result.Data) {
		return false, nil
	}

	for i := range originalData {
		if originalData[i] != result.Data[i] {
			return false, nil
		}
	}

	return true, nil
}

// SortLabels returns label names sorted by their offset
func SortLabels(labels map[int]string) []string {
	offsets := make([]int, 0, len(labels))
	for off := range labels {
		offsets = append(offsets, off)
	}
	sort.Ints(offsets)

	result := make([]string, 0, len(labels))
	for _, off := range offsets {
		result = append(result, labels[off])
	}
	return result
}
