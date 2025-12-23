package cmd

import (
	"fmt"
	"strconv"

	"agetools/pkg/scflow"
	"github.com/spf13/cobra"
)

var scflowCmd = &cobra.Command{
	Use:   "scflow <file.txt> [command] [args...]",
	Short: "Analyze SC scenario file control and data flow",
	Long: `Analyze control flow and data flow in disassembled SC scenario files.

This tool helps with reverse engineering by providing queries like:
- Character ID lookup for dialogue lines
- Variable assignment tracing
- Function call tracking

Examples:
  agetools scflow SC0000.txt analyze                    # Analyze file
  agetools scflow SC0000.txt char-id 841               # Find character at line 841
  agetools scflow SC0000.txt trace-var "local-int:0" 100  # Trace variable at line 100
  agetools scflow SC0000.txt calls "label_000C0248"    # Find all calls to function`,
	Args: cobra.MinimumNArgs(1),
	RunE: runSCFlow,
}

func init() {
	rootCmd.AddCommand(scflowCmd)
}

func runSCFlow(cmd *cobra.Command, args []string) error {
	if len(args) < 1 {
		return fmt.Errorf("file path required")
	}

	filepath := args[0]

	// Create and run analyzer
	analyzer := scflow.NewAnalyzer(filepath)
	fmt.Printf("Analyzing %s...\n", filepath)

	if err := analyzer.Analyze(); err != nil {
		return fmt.Errorf("analysis failed: %w", err)
	}

	fmt.Printf("Analysis complete:\n")
	fmt.Printf("  Instructions: %d\n", len(analyzer.Instructions))
	fmt.Printf("  Labels: %d\n", len(analyzer.Labels))
	fmt.Printf("  Variables tracked: %d\n", len(analyzer.Variables))
	fmt.Printf("  Function calls: %d\n", len(analyzer.FunctionCalls))

	// Handle subcommands
	if len(args) < 2 {
		return nil
	}

	subcommand := args[1]

	switch subcommand {
	case "char-id":
		if len(args) < 3 {
			return fmt.Errorf("char-id requires line number")
		}
		return handleCharID(analyzer, args[2])

	case "trace-var":
		if len(args) < 4 {
			return fmt.Errorf("trace-var requires variable name and line number")
		}
		return handleTraceVar(analyzer, args[2], args[3])

	case "calls":
		if len(args) < 3 {
			return fmt.Errorf("calls requires function label")
		}
		return handleCalls(analyzer, args[2])

	case "assigns":
		if len(args) < 3 {
			return fmt.Errorf("assigns requires variable name")
		}
		return handleAssigns(analyzer, args[2])

	default:
		return fmt.Errorf("unknown subcommand: %s", subcommand)
	}
}

// handleCharID handles character ID queries
func handleCharID(analyzer *scflow.Analyzer, lineStr string) error {
	lineNum, err := strconv.Atoi(lineStr)
	if err != nil {
		return fmt.Errorf("invalid line number: %w", err)
	}

	charID, explanation := analyzer.QueryCharacterIDUsingCFG(lineNum)

	fmt.Printf("\nCharacter ID: %d\n", charID)
	fmt.Println("\nCFG-based Trace:")
	for _, line := range explanation {
		fmt.Println(line)
	}

	return nil
}

// handleTraceVar handles variable tracing
func handleTraceVar(analyzer *scflow.Analyzer, varName, lineStr string) error {
	lineNum, err := strconv.Atoi(lineStr)
	if err != nil {
		return fmt.Errorf("invalid line number: %w", err)
	}

	fmt.Printf("\nTracing %s at line %d:\n", varName, lineNum)

	trace := analyzer.TraceVariableBackwards(varName, lineNum)
	if len(trace) == 0 {
		fmt.Println("  (no trace found)")
		return nil
	}

	for _, line := range trace {
		fmt.Printf("  %s\n", line)
	}

	return nil
}

// handleCalls handles function call queries
func handleCalls(analyzer *scflow.Analyzer, funcLabel string) error {
	calls := analyzer.FindCallsTo(funcLabel)

	fmt.Printf("\nCalls to %s (%d found):\n", funcLabel, len(calls))

	for _, call := range calls {
		fmt.Printf("  Line %5d: %s\n", call.LineNum, call.Raw)
	}

	return nil
}

// handleAssigns handles variable assignment queries
func handleAssigns(analyzer *scflow.Analyzer, varName string) error {
	assigns := analyzer.FindAssignmentsTo(varName)

	fmt.Printf("\nAssignments to %s (%d found):\n", varName, len(assigns))

	for _, assign := range assigns {
		fmt.Printf("  Line %5d: %s = %s\n", assign.LineNum, varName, assign.AssignedFrom)
	}

	return nil
}
