package scflow

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
)

// BasicBlock represents a basic block in the control flow graph
type BasicBlock struct {
	Label        string
	StartLine    int
	EndLine      int
	Instructions []*Instruction
	Successors   []string // Labels of successor blocks
	Predecessors []string // Labels of predecessor blocks
}

// CFG represents the control flow graph
type CFG struct {
	Blocks        map[string]*BasicBlock // label -> block
	LineToBlock   map[int]string         // line -> block label
	CallGraph     map[string][]string    // func label -> called functions
	ReverseGraph  map[string][]string    // func label -> functions that call it
}

// BuildCFG builds a control flow graph from instructions
// Simple model: each label is a block, blocks end at labels, jmp creates control flow links
func (a *Analyzer) BuildCFG() *CFG {
	cfg := &CFG{
		Blocks:       make(map[string]*BasicBlock),
		LineToBlock:  make(map[int]string),
		CallGraph:    make(map[string][]string),
		ReverseGraph: make(map[string][]string),
	}

	// First pass: create blocks for each label and assign instructions
	blocksByLabel := make(map[string]*BasicBlock)
	var sortedLines []int
	for line := range a.Instructions {
		sortedLines = append(sortedLines, line)
	}

	// Sort lines
	for i := 0; i < len(sortedLines); i++ {
		for j := i + 1; j < len(sortedLines); j++ {
			if sortedLines[i] > sortedLines[j] {
				sortedLines[i], sortedLines[j] = sortedLines[j], sortedLines[i]
			}
		}
	}

	// Create blocks for each unique label
	labelOrder := make([]string, 0)
	for _, lineNum := range sortedLines {
		instr := a.Instructions[lineNum]
		if _, exists := blocksByLabel[instr.Label]; !exists {
			blocksByLabel[instr.Label] = &BasicBlock{
				Label:        instr.Label,
				Instructions: make([]*Instruction, 0),
				Successors:   make([]string, 0),
				Predecessors: make([]string, 0),
			}
			labelOrder = append(labelOrder, instr.Label)
		}
	}

	// Assign instructions to blocks and map line to block
	for _, lineNum := range sortedLines {
		instr := a.Instructions[lineNum]
		block := blocksByLabel[instr.Label]
		block.Instructions = append(block.Instructions, instr)
		cfg.LineToBlock[lineNum] = instr.Label

		// Set block start/end lines
		if block.StartLine == 0 {
			block.StartLine = lineNum
		}
		block.EndLine = lineNum
	}

	// Second pass: build successor relationships based on jmp instructions
	for _, block := range blocksByLabel {
		if len(block.Instructions) == 0 {
			continue
		}

		lastInstr := block.Instructions[len(block.Instructions)-1]

		switch lastInstr.Opcode {
		case "jmp":
			// Unconditional jump - only one successor
			target := ""
			for _, arg := range lastInstr.Args {
				if strings.HasPrefix(arg, "label_") {
					target = arg
					break
				}
			}
			if target != "" {
				block.Successors = append(block.Successors, target)
			}

		case "jcc":
			// Conditional jump - two successors: true (jump target) and false (fallthrough)
			target := ""
			for _, arg := range lastInstr.Args {
				if strings.HasPrefix(arg, "label_") {
					target = arg
					break
				}
			}
			if target != "" {
				block.Successors = append(block.Successors, target)
				// Add fallthrough to next label
				blockIdx := -1
				for i, l := range labelOrder {
					if l == block.Label {
						blockIdx = i
						break
					}
				}
				if blockIdx >= 0 && blockIdx+1 < len(labelOrder) {
					block.Successors = append(block.Successors, labelOrder[blockIdx+1])
				}
			}

		case "call":
			// Call continues to next block
			blockIdx := -1
			for i, l := range labelOrder {
				if l == block.Label {
					blockIdx = i
					break
				}
			}
			if blockIdx >= 0 && blockIdx+1 < len(labelOrder) {
				block.Successors = append(block.Successors, labelOrder[blockIdx+1])
			}

			// Track in call graph
			if len(lastInstr.Args) > 0 {
				calledFunc := lastInstr.Args[0]
				cfg.CallGraph[block.Label] = append(cfg.CallGraph[block.Label], calledFunc)

				// Build reverse graph
				if _, exists := cfg.ReverseGraph[calledFunc]; !exists {
					cfg.ReverseGraph[calledFunc] = make([]string, 0)
				}
				cfg.ReverseGraph[calledFunc] = append(cfg.ReverseGraph[calledFunc], block.Label)
			}

		case "ret", "exit":
			// Function ends - no successors

		default:
			// Normal instruction - fall through to next block
			blockIdx := -1
			for i, l := range labelOrder {
				if l == block.Label {
					blockIdx = i
					break
				}
			}
			if blockIdx >= 0 && blockIdx+1 < len(labelOrder) {
				block.Successors = append(block.Successors, labelOrder[blockIdx+1])
			}
		}
	}

	cfg.Blocks = blocksByLabel

	// Build predecessor relationships
	for label, block := range cfg.Blocks {
		for _, succ := range block.Successors {
			if succBlock, exists := cfg.Blocks[succ]; exists {
				succBlock.Predecessors = append(succBlock.Predecessors, label)
			}
		}
	}

	return cfg
}


// QueryCharacterIDUsingCFG uses CFG to trace character ID more accurately
func (a *Analyzer) QueryCharacterIDUsingCFG(dialogueLine int) (int, []string) {
	cfg := a.BuildCFG()
	var explanation []string
	explanation = append(explanation, fmt.Sprintf("Tracing character ID for dialogue at line %d using CFG", dialogueLine))

	// Find which block contains the dialogue
	dialogueBlock := ""
	if blockLabel, exists := cfg.LineToBlock[dialogueLine]; exists {
		dialogueBlock = blockLabel
		explanation = append(explanation, fmt.Sprintf("  Dialogue in block: %s", blockLabel))
	} else {
		explanation = append(explanation, "  Could not find dialogue block")
		return 0, explanation
	}

	// Check if the dialogue line itself has a narration flag (first arg is 0)
	// This applies to show-text, display-furigana, and similar instructions
	if instr, exists := a.Instructions[dialogueLine]; exists && len(instr.Args) > 0 {
		if instr.Args[0] == "0" && isDialogueRelatedOpcode(instr.Opcode) {
			explanation = append(explanation, fmt.Sprintf("  Dialogue line has %s 0 (narrator)", instr.Opcode))
			return 0, explanation
		}
	}

	// Work backwards through predecessors to find setup calls
	visited := make(map[string]bool)
	charID := queryCharIDInBlock(cfg, dialogueBlock, visited, &explanation)

	return charID, explanation
}

// queryCharIDInBlock recursively searches for character ID in a block and its predecessors
func queryCharIDInBlock(cfg *CFG, blockLabel string, visited map[string]bool, explanation *[]string) int {
	if visited[blockLabel] {
		return 0
	}
	visited[blockLabel] = true

	block, exists := cfg.Blocks[blockLabel]
	if !exists {
		return 0
	}

	*explanation = append(*explanation, fmt.Sprintf("    Examining block %s", blockLabel))

	// Look for show-text instruction in this block (dialogue line)
	var dialogueLineInBlock int = -1
	for i, instr := range block.Instructions {
		if instr.Opcode == "show-text" {
			dialogueLineInBlock = i
			break
		}
	}

	// If this block has dialogue, look backwards from it for character ID
	if dialogueLineInBlock >= 0 {
		*explanation = append(*explanation, fmt.Sprintf("      Found dialogue at line %d in this block", block.Instructions[dialogueLineInBlock].LineNum))

		// Search forward from the block start to find character ID assignments before dialogue
		// This captures the first assignment which is typically the character ID
		var foundCharID int = -1
		for i := 0; i < dialogueLineInBlock; i++ {
			instr := block.Instructions[i]
			if charID := extractCharacterID(instr); charID >= 0 {
				foundCharID = charID
				*explanation = append(*explanation, fmt.Sprintf("      Found character ID %d at line %d: %s",
					charID, instr.LineNum, instr.Raw))
				break
			}
		}

		if foundCharID >= 0 {
			return foundCharID
		}

		// If not found in current block, search in all predecessors recursively
		for _, predLabel := range block.Predecessors {
			if charID := queryCharIDInBlock(cfg, predLabel, visited, explanation); charID >= 0 {
				return charID
			}
		}
	}

	// If no dialogue or no ID found in this block, search this block's full instruction list
	// This handles cases where character ID is set earlier in the block
	var foundCharID int = -1
	for _, instr := range block.Instructions {
		if charID := extractCharacterID(instr); charID >= 0 {
			foundCharID = charID
			// Don't return immediately - keep looking to find the LAST (most recent) assignment
		}
	}
	if foundCharID >= 0 {
		*explanation = append(*explanation, fmt.Sprintf("      Found character ID %d in block %s instructions", foundCharID, blockLabel))
		return foundCharID
	}

	// If still not found, recursively search in predecessors
	for _, predLabel := range block.Predecessors {
		if charID := queryCharIDInBlock(cfg, predLabel, visited, explanation); charID >= 0 {
			return charID
		}
	}

	return 0
}
// extractCharacterID extracts character ID from an instruction
func extractCharacterID(instr *Instruction) int {
	// Look for: mov <character-related-var> <number>
	// Focus on variables that typically store character IDs
	regex := regexp.MustCompile(`mov\s+(?:local-ptr:0|global-int:1566494|global-int:1881613)\s+(\d+)`)
	if match := regex.FindStringSubmatch(instr.Raw); match != nil {
		charID, _ := strconv.Atoi(match[1])
		// Return the character ID (can be 0 for narrator, or any valid ID)
		return charID
	}
	return -1
}

// GetBlockInfo returns information about a block
func (cfg *CFG) GetBlockInfo(label string) string {
	block, exists := cfg.Blocks[label]
	if !exists {
		return "Block not found"
	}

	info := fmt.Sprintf("Block %s\n", label)
	info += fmt.Sprintf("  Lines: %d-%d (%d instructions)\n", block.StartLine, block.EndLine, len(block.Instructions))
	info += fmt.Sprintf("  Successors: %v\n", block.Successors)
	info += fmt.Sprintf("  Predecessors: %v\n", block.Predecessors)

	return info
}

// isDialogueRelatedOpcode checks if an opcode is related to dialogue/text display
func isDialogueRelatedOpcode(opcode string) bool {
	switch opcode {
	case "show-text", "display-furigana":
		return true
	default:
		return false
	}
}
