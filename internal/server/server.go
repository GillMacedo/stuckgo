// Package server exposes goroutine dump review as MCP tools.
package server

import (
	"context"
	"errors"
	"fmt"
	"os"

	"github.com/GillMacedo/stuckgo/internal/dump"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// Large services really do produce big dumps, but a stray huge file shouldn't fill memory.
const maxDumpBytes = 256 << 20

// ReviewInput is the input of go_goroutine_review.
type ReviewInput struct {
	Dump string `json:"dump,omitempty" jsonschema:"Goroutine dump text. Give this or path"`
	Path string `json:"path,omitempty" jsonschema:"Path to a file holding a goroutine dump. Prefer this for large dumps. Give this or dump"`
}

// CompareInput is the input of go_goroutine_compare.
type CompareInput struct {
	Before     string `json:"before,omitempty" jsonschema:"The earlier goroutine dump's text"`
	BeforePath string `json:"before_path,omitempty" jsonschema:"Path to a file with the earlier dump"`
	After      string `json:"after,omitempty" jsonschema:"The later goroutine dump's text"`
	AfterPath  string `json:"after_path,omitempty" jsonschema:"Path to a file with the later dump"`
}

func no() *bool { value := false; return &value }

// New returns the MCP server.
func New(version string) *mcp.Server {
	server := mcp.NewServer(&mcp.Implementation{Name: "stuckgo", Version: version}, nil)
	annotations := &mcp.ToolAnnotations{ReadOnlyHint: true, DestructiveHint: no(), IdempotentHint: true, OpenWorldHint: no()}
	mcp.AddTool(server, &mcp.Tool{
		Name:  "go_goroutine_review",
		Title: "Review a Go goroutine dump",
		Description: "Finds what a stuck or leaking Go program is waiting on, from a goroutine dump: /debug/pprof/goroutine?debug=2 or debug=1, a panic traceback, or SIGQUIT output. Groups identical stacks, separates normal idle goroutines, and flags channel leaks, lock pile-ups with possible holders, WaitGroups that never finish, full database pools, HTTP calls with no timeout and nil channels. " +
			"It only reads the text you give it and never connects to the program. Findings are leads.",
		Annotations: annotations,
	}, review)
	mcp.AddTool(server, &mcp.Tool{
		Name:        "go_goroutine_compare",
		Title:       "Compare two Go goroutine dumps",
		Description: "Compares two goroutine dumps of one Go program taken some time apart, and shows which stacks are growing and which goroutine IDs never moved, which is stronger evidence of a leak than one dump. It never connects to the program.",
		Annotations: annotations,
	}, compare)
	return server
}

// Read returns a dump from an inline value or a file.
func Read(inline, path string) (*dump.Dump, error) {
	if (inline == "") == (path == "") {
		return nil, errors.New("give the dump's text or a path to a file, not both or neither")
	}
	text := inline
	if path != "" {
		info, err := os.Stat(path)
		if err != nil {
			return nil, err
		}
		if info.Size() > maxDumpBytes {
			return nil, fmt.Errorf("%s is larger than %d MB", path, maxDumpBytes>>20)
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return nil, err
		}
		text = string(data)
	}
	return dump.Parse(text)
}

func review(_ context.Context, _ *mcp.CallToolRequest, in ReviewInput) (*mcp.CallToolResult, dump.Review, error) {
	parsed, err := Read(in.Dump, in.Path)
	if err != nil {
		return nil, dump.Review{}, err
	}
	return nil, dump.Analyze(parsed), nil
}

func compare(_ context.Context, _ *mcp.CallToolRequest, in CompareInput) (*mcp.CallToolResult, dump.Comparison, error) {
	before, err := Read(in.Before, in.BeforePath)
	if err != nil {
		return nil, dump.Comparison{}, fmt.Errorf("before: %w", err)
	}
	after, err := Read(in.After, in.AfterPath)
	if err != nil {
		return nil, dump.Comparison{}, fmt.Errorf("after: %w", err)
	}
	return nil, dump.Compare(before, after), nil
}
