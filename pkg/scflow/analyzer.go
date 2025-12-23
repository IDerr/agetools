package scflow

import (
	"bufio"
	"fmt"
	"os"
	"regexp"
	"strconv"
	"strings"
)

// Instruction represents a parsed instruction from SC file
type Instruction struct {
	LineNum int
	Label   string
	Opcode  string
	Args    []string
	Raw     string
}

// Variable tracks assignments to a variable
type Variable struct {
	Name        string
	Assignments []Assignment
}

// Assignment represents a value assignment to a variable
type Assignment struct {
	LineNum      int
	AssignedFrom string
}

// Analyzer performs flow and dataflow analysis on SC files
type Analyzer struct {
	FilePath     string
	Lines        []string
	Instructions map[int]*Instruction
	Labels       map[string]int
	Variables    map[string]*Variable
	FunctionCalls map[string][]int // function label -> line numbers
}

// NewAnalyzer creates a new analyzer for an SC file
func NewAnalyzer(filepath string) *Analyzer {
	return &Analyzer{
		FilePath:      filepath,
		Lines:         []string{},
		Instructions:  make(map[int]*Instruction),
		Labels:        make(map[string]int),
		Variables:     make(map[string]*Variable),
		FunctionCalls: make(map[string][]int),
	}
}

// ReadFile reads the SC file
func (a *Analyzer) ReadFile() error {
	file, err := os.Open(a.FilePath)
	if err != nil {
		return fmt.Errorf("failed to open file: %w", err)
	}
	defer file.Close()

	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		a.Lines = append(a.Lines, scanner.Text())
	}

	if err := scanner.Err(); err != nil {
		return fmt.Errorf("error reading file: %w", err)
	}

	return nil
}

// Parse parses all instructions and labels
func (a *Analyzer) Parse() error {
	currentLabel := "_start"

	labelRegex := regexp.MustCompile(`^(label_[0-9A-Fa-f]+):`)

	for lineNum, rawLine := range a.Lines {
		line := strings.TrimSpace(rawLine)

		// Skip empty lines and metadata
		if line == "" || strings.HasPrefix(line, "==") || strings.HasPrefix(line, "signature") ||
			strings.HasPrefix(line, "local_vars") {
			continue
		}

		// Check for labels
		if match := labelRegex.FindStringSubmatch(line); match != nil {
			currentLabel = match[1]
			a.Labels[currentLabel] = lineNum
			continue
		}

		// Parse instructions (they start with spaces in raw line)
		if len(rawLine) > 0 && (rawLine[0] == ' ' || rawLine[0] == '\t') {
			parts := strings.Fields(line)
			if len(parts) == 0 {
				continue
			}

			opcode := parts[0]
			args := parts[1:]

			instr := &Instruction{
				LineNum: lineNum,
				Label:   currentLabel,
				Opcode:  opcode,
				Args:    args,
				Raw:     line,
			}

			a.Instructions[lineNum] = instr

			// Track function calls
			if opcode == "call" && len(args) > 0 {
				a.FunctionCalls[args[0]] = append(a.FunctionCalls[args[0]], lineNum)
			}
		}
	}

	return nil
}

// BuildDataflow analyzes variable assignments
func (a *Analyzer) BuildDataflow() {
	for lineNum, instr := range a.Instructions {
		opcode := instr.Opcode
		args := instr.Args

		switch opcode {
		case "mov":
			if len(args) >= 2 {
				dest := args[0]
				src := strings.Join(args[1:], " ")
				a.addVariableAssignment(dest, lineNum, src)
			}

		case "lookup-array":
			if len(args) >= 3 {
				dest := args[0]
				array := args[1]
				index := args[2]
				a.addVariableAssignment(dest, lineNum, fmt.Sprintf("%s[%s]", array, index))
			}

		case "set-string":
			if len(args) >= 2 {
				dest := args[0]
				src := strings.Join(args[1:], " ")
				a.addVariableAssignment(dest, lineNum, src)
			}

		case "lookup-array-2d":
			if len(args) >= 5 {
				dest := args[0]
				array := args[1]
				idx1 := args[2]
				idx2 := args[4]
				a.addVariableAssignment(dest, lineNum, fmt.Sprintf("%s[%s][%s]", array, idx1, idx2))
			}
		}
	}
}

// addVariableAssignment adds an assignment to a variable
func (a *Analyzer) addVariableAssignment(varName string, lineNum int, assignedFrom string) {
	if _, exists := a.Variables[varName]; !exists {
		a.Variables[varName] = &Variable{Name: varName}
	}
	a.Variables[varName].Assignments = append(a.Variables[varName].Assignments,
		Assignment{LineNum: lineNum, AssignedFrom: assignedFrom})
}

// Analyze runs complete analysis
func (a *Analyzer) Analyze() error {
	if err := a.ReadFile(); err != nil {
		return err
	}

	if err := a.Parse(); err != nil {
		return err
	}

	a.BuildDataflow()

	return nil
}

// TraceVariableBackwards traces a variable's value chain backwards
func (a *Analyzer) TraceVariableBackwards(varName string, atLine int) []string {
	var trace []string

	if _, exists := a.Variables[varName]; !exists {
		return trace
	}

	// Get last assignment before this line
	var lastAssignment *Assignment
	for _, assign := range a.Variables[varName].Assignments {
		if assign.LineNum < atLine {
			lastAssignment = &assign
		} else {
			break
		}
	}

	if lastAssignment == nil {
		return trace
	}

	trace = append(trace, fmt.Sprintf("%s = %s", varName, lastAssignment.AssignedFrom))

	// Continue tracing source variables
	visited := make(map[string]bool)
	toVisit := []struct {
		line  int
		value string
	}{
		{lastAssignment.LineNum, lastAssignment.AssignedFrom},
	}

	for len(toVisit) > 0 {
		current := toVisit[0]
		toVisit = toVisit[1:]

		if visited[current.value] {
			continue
		}
		visited[current.value] = true

		// Extract variable names from value
		varRegex := regexp.MustCompile(`(local-\w+:\d+|global-\w+:\d+|\w+)`)
		for _, match := range varRegex.FindAllString(current.value, -1) {
			srcVar := match

			if variable, exists := a.Variables[srcVar]; exists {
				// Get value at this line
				var srcAssignment *Assignment
				for _, assign := range variable.Assignments {
					if assign.LineNum < current.line {
						srcAssignment = &assign
					} else {
						break
					}
				}

				if srcAssignment != nil && !visited[srcAssignment.AssignedFrom] {
					trace = append(trace, fmt.Sprintf("%s = %s", srcVar, srcAssignment.AssignedFrom))
					toVisit = append(toVisit, struct {
						line  int
						value string
					}{srcAssignment.LineNum, srcAssignment.AssignedFrom})
				}
			}
		}
	}

	return trace
}

// QueryCharacterIDForDialogue finds character ID for a dialogue line
func (a *Analyzer) QueryCharacterIDForDialogue(dialogueLine int) (int, []string) {
	var explanation []string
	explanation = append(explanation, fmt.Sprintf("Tracing character ID for dialogue at line %d", dialogueLine))

	// Find nearest call label_000C0248 before this line
	var setupCallLine *int
	searchStart := dialogueLine - 200
	if searchStart < 0 {
		searchStart = 0
	}

	for i := dialogueLine - 1; i >= searchStart; i-- {
		if instr, exists := a.Instructions[i]; exists {
			if instr.Opcode == "call" && len(instr.Args) > 0 && instr.Args[0] == "label_000C0248" {
				setupCallLine = &i
				explanation = append(explanation, fmt.Sprintf("  Found setup call at line %d", i))
				break
			}
		}
	}

	if setupCallLine == nil {
		explanation = append(explanation, "  No setup call found, defaulting to 0 (Narrator)")
		return 0, explanation
	}

	// Look backwards from setup call for character ID
	// We need to find the mov local-ptr:0 X that is NOT 0 (since 0 is state variable)
	for i := *setupCallLine - 1; i > *setupCallLine-50 && i >= 0; i-- {
		if instr, exists := a.Instructions[i]; exists {
			// Look for: mov local-ptr:0 <number>
			regex := regexp.MustCompile(`mov\s+local-ptr:0\s+(\d+)`)
			if match := regex.FindStringSubmatch(instr.Raw); match != nil {
				charID, _ := strconv.Atoi(match[1])
				// Skip the 0 assignment (that's for state variable), find the actual character ID
				if charID != 0 {
					explanation = append(explanation, fmt.Sprintf("  Found character ID at line %d: %s", i, instr.Raw))
					explanation = append(explanation, a.GetInstructionContext(i, 3)...)
					return charID, explanation
				}
			}

			// Stop at new dialogue block
			if strings.Contains(instr.Raw, "mov global-int:26149 0") {
				explanation = append(explanation, fmt.Sprintf("  Reached dialogue block start at line %d", i))
				break
			}
		}
	}

	explanation = append(explanation, "  No explicit character ID found, defaulting to 0 (Narrator)")
	return 0, explanation
}

// GetInstructionContext returns surrounding context for an instruction
func (a *Analyzer) GetInstructionContext(lineNum int, contextLines int) []string {
	var result []string

	start := lineNum - contextLines
	if start < 0 {
		start = 0
	}

	end := lineNum + contextLines + 1
	if end > len(a.Lines) {
		end = len(a.Lines)
	}

	for i := start; i < end; i++ {
		marker := "     "
		if i == lineNum {
			marker = " >>> "
		}
		result = append(result, fmt.Sprintf("%5d%s%s", i, marker, a.Lines[i]))
	}

	return result
}

// FindAssignmentsTo finds all assignments to a variable
func (a *Analyzer) FindAssignmentsTo(varName string) []struct {
	LineNum      int
	AssignedFrom string
	Raw          string
} {
	var result []struct {
		LineNum      int
		AssignedFrom string
		Raw          string
	}

	if variable, exists := a.Variables[varName]; exists {
		for _, assign := range variable.Assignments {
			if instr, exists := a.Instructions[assign.LineNum]; exists {
				result = append(result, struct {
					LineNum      int
					AssignedFrom string
					Raw          string
				}{
					LineNum:      assign.LineNum,
					AssignedFrom: assign.AssignedFrom,
					Raw:          instr.Raw,
				})
			}
		}
	}

	return result
}

// FindCallsTo finds all function calls to a label
func (a *Analyzer) FindCallsTo(funcLabel string) []struct {
	LineNum int
	Raw     string
} {
	var result []struct {
		LineNum int
		Raw     string
	}

	for _, lineNum := range a.FunctionCalls[funcLabel] {
		if instr, exists := a.Instructions[lineNum]; exists {
			result = append(result, struct {
				LineNum int
				Raw     string
			}{
				LineNum: lineNum,
				Raw:     instr.Raw,
			})
		}
	}

	return result
}
