// Command stuckgo finds what a stuck Go program is waiting on, from a goroutine dump.
package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"

	"github.com/GillMacedo/stuckgo/internal/dump"
	"github.com/GillMacedo/stuckgo/internal/server"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

const version = "0.1.0"

const usage = `Usage:
  stuckgo review <dump-file>           Find what the program is stuck on. Use - for stdin
  stuckgo compare <before> <after>     Compare two dumps of one program
  stuckgo serve                        Run as an MCP server over stdio
  stuckgo version

Options for review and compare:
  --json             Print JSON
  --fail-on LEVEL    review exits 1 at this severity: high, medium, low or none (default none)
`

func main() {
	os.Exit(run(os.Args[1:], os.Stdin, os.Stdout, os.Stderr))
}

func run(args []string, stdin io.Reader, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		fmt.Fprint(stderr, usage)
		return 2
	}
	var err error
	code := 0
	switch args[0] {
	case "review":
		code, err = reviewCommand(args[1:], stdin, stdout)
	case "compare":
		err = compareCommand(args[1:], stdin, stdout)
	case "serve":
		err = server.New(version).Run(context.Background(), &mcp.StdioTransport{})
	case "version":
		fmt.Fprintln(stdout, version)
	default:
		err = fmt.Errorf("unknown command %q\n%s", args[0], usage)
	}
	if err != nil {
		fmt.Fprintf(stderr, "stuckgo: %v\n", err)
		return 2
	}
	return code
}

func load(path string, stdin io.Reader) (*dump.Dump, error) {
	if path != "-" {
		return server.Read("", path)
	}
	data, err := io.ReadAll(stdin)
	if err != nil {
		return nil, err
	}
	return dump.Parse(string(data))
}

func parseFlags(name string, args []string, wanted int) ([]string, bool, string, error) {
	flags := flag.NewFlagSet(name, flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	asJSON := flags.Bool("json", false, "")
	failOn := flags.String("fail-on", "none", "")
	var paths []string
	for rest := args; ; {
		if err := flags.Parse(rest); err != nil {
			return nil, false, "", err
		}
		if flags.NArg() == 0 {
			break
		}
		paths = append(paths, flags.Arg(0))
		rest = flags.Args()[1:]
	}
	if len(paths) != wanted {
		return nil, false, "", fmt.Errorf("%s takes %d dump files\n%s", name, wanted, usage)
	}
	return paths, *asJSON, *failOn, nil
}

func reviewCommand(args []string, stdin io.Reader, stdout io.Writer) (int, error) {
	paths, asJSON, failOn, err := parseFlags("review", args, 1)
	if err != nil {
		return 0, err
	}
	levels := map[string][]string{"none": {}, "high": {dump.High}, "medium": {dump.High, dump.Medium}, "low": {dump.High, dump.Medium, dump.Low}}
	failing, ok := levels[failOn]
	if !ok {
		return 0, errors.New("--fail-on must be high, medium, low or none")
	}
	parsed, err := load(paths[0], stdin)
	if err != nil {
		return 0, err
	}
	result := dump.Analyze(parsed)
	if asJSON {
		if err := printJSON(stdout, result); err != nil {
			return 0, err
		}
	} else {
		printReview(stdout, result)
	}
	if result.HasFindingAt(failing...) {
		return 1, nil
	}
	return 0, nil
}

func compareCommand(args []string, stdin io.Reader, stdout io.Writer) error {
	paths, asJSON, _, err := parseFlags("compare", args, 2)
	if err != nil {
		return err
	}
	if paths[0] == "-" && paths[1] == "-" {
		return errors.New("only one dump can come from stdin")
	}
	before, err := load(paths[0], stdin)
	if err != nil {
		return fmt.Errorf("before: %w", err)
	}
	after, err := load(paths[1], stdin)
	if err != nil {
		return fmt.Errorf("after: %w", err)
	}
	result := dump.Compare(before, after)
	if asJSON {
		return printJSON(stdout, result)
	}
	printComparison(stdout, result)
	return nil
}

func printJSON(stdout io.Writer, value any) error {
	encoder := json.NewEncoder(stdout)
	encoder.SetIndent("", "  ")
	return encoder.Encode(value)
}

func printReview(stdout io.Writer, result dump.Review) {
	if result.Fatal != "" {
		fmt.Fprintln(stdout, result.Fatal)
	}
	noun := "goroutines"
	if result.Total == 1 {
		noun = "goroutine"
	}
	fmt.Fprintf(stdout, "%d %s, %d normally idle\n", result.Total, noun, result.Expected)
	printed := false
	for _, group := range result.Groups {
		if group.Expected {
			continue
		}
		if !printed {
			fmt.Fprintln(stdout, "\nGroups:")
			printed = true
		}
		wait := ""
		if group.MaxWaitMinutes > 0 {
			wait = fmt.Sprintf(", up to %d min", group.MaxWaitMinutes)
		}
		fmt.Fprintf(stdout, "  %6d  %s%s  %s\n", group.Count, group.State, wait, group.Where)
	}
	if hidden := result.GroupCount - len(result.Groups); hidden > 0 {
		fmt.Fprintf(stdout, "  and %d smaller groups\n", hidden)
	}
	for _, finding := range result.Findings {
		fmt.Fprintf(stdout, "\n%-6s %s %s\n  %s\n", upper(finding.Severity), finding.ID, finding.Where, finding.Message)
		if finding.Try != "" {
			fmt.Fprintf(stdout, "  Try: %s\n", finding.Try)
		}
	}
	if len(result.Findings) == 0 {
		fmt.Fprintln(stdout, "\nNothing here looks stuck. Take a second dump and compare, because a slow leak can look normal once.")
	}
	fmt.Fprintln(stdout)
	for _, note := range result.Notes {
		fmt.Fprintln(stdout, note)
	}
	fmt.Fprintln(stdout, "These are leads from one moment in time, so check them against the code.")
}

func upper(severity string) string {
	switch severity {
	case dump.High:
		return "HIGH"
	case dump.Medium:
		return "MEDIUM"
	}
	return "LOW"
}

func printComparison(stdout io.Writer, result dump.Comparison) {
	fmt.Fprintf(stdout, "Goroutines: %d -> %d (%+d)\n", result.Before, result.After, result.After-result.Before)
	if len(result.Growing) == 0 {
		fmt.Fprintln(stdout, "\nNo stack grew.")
	}
	printChanges(stdout, "Growing", result.Growing)
	printChanges(stdout, "Shrinking", result.Shrinking)
	if len(result.Parked) > 0 {
		fmt.Fprintln(stdout, "\nSame goroutine IDs in the same place in both dumps:")
		for _, item := range result.Parked {
			wait := ""
			if item.MaxWaitMinutes > 0 {
				wait = fmt.Sprintf(", up to %d min", item.MaxWaitMinutes)
			}
			fmt.Fprintf(stdout, "  %6d  %s%s  %s\n", item.Count, item.State, wait, item.Where)
		}
	}
	if result.ParkedElsewhere > 0 {
		fmt.Fprintf(stdout, "\n%d other goroutines stayed in place at stacks that aren't growing, as long-running loops do.\n", result.ParkedElsewhere)
	}
	printFindings(stdout, "Fixed", result.Fixed)
	printFindings(stdout, "New", result.New)
	printFindings(stdout, "Still there", result.Remaining)
	fmt.Fprintln(stdout)
	for _, note := range result.Notes {
		fmt.Fprintln(stdout, note)
	}
}

func printChanges(stdout io.Writer, title string, changes []dump.Change) {
	if len(changes) == 0 {
		return
	}
	fmt.Fprintf(stdout, "\n%s:\n", title)
	for _, change := range changes {
		fmt.Fprintf(stdout, "  %+6d  %d -> %d  %s  %s\n", change.Delta, change.Before, change.After, change.State, change.Where)
	}
}

func printFindings(stdout io.Writer, title string, findings []dump.Finding) {
	if len(findings) == 0 {
		return
	}
	fmt.Fprintf(stdout, "\n%s:\n", title)
	for _, finding := range findings {
		fmt.Fprintf(stdout, "  %s %s %s: %d\n", upper(finding.Severity), finding.ID, finding.Where, finding.Count)
	}
}
