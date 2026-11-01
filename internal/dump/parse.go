// Package dump reads Go goroutine dumps and finds what looks stuck.
package dump

import (
	"errors"
	"regexp"
	"strconv"
	"strings"
)

// Formats of a dump.
const (
	FormatTraceback = "traceback" // debug=2, a panic, or SIGQUIT
	FormatProfile   = "profile"   // debug=1
)

// Frame is one line of a stack.
type Frame struct {
	Func string `json:"func"`
	File string `json:"file,omitempty"`
	Line int    `json:"line,omitempty"`
	Args string `json:"args,omitempty"`
}

// Goroutine is one goroutine, or in a debug=1 dump, a group with one stack.
type Goroutine struct {
	ID          int
	Count       int
	State       string
	WaitMinutes int
	Locked      bool
	Leaked      bool
	Frames      []Frame
	CreatedBy   string
	CreatedAt   string
	Elided      bool
}

// Dump is a parsed goroutine dump.
type Dump struct {
	Format     string
	Goroutines []Goroutine
	Fatal      string
	Signal     string
	CutOff     bool
}

var (
	timestamp   = regexp.MustCompile(`^\d{4}-\d{2}-\d{2}[T ]\d{2}:\d{2}:\d{2}(?:[.,]\d+)?(?:Z|[+-]\d{2}:?\d{2})? `)
	header      = regexp.MustCompile(`^goroutine (\d+)(?: gp=\S+)?(?: m=\S+)?(?: mp=\S+)? \[(.*?)\](?: \{.*\})?:$`)
	profileHead = regexp.MustCompile(`^(goroutine|goroutineleak) profile: total (\d+)$`)
	profileRow  = regexp.MustCompile(`^(\d+) @(?: 0x[0-9a-f]+)+$`)
	profileLine = regexp.MustCompile(`^#\s+0x[0-9a-f]+\s+(\S+?)(?:\+0x[0-9a-f]+)?\s+(.+):(\d+)$`)
	fileLine    = regexp.MustCompile(`^(.+):(\d+)(?: \+0x[0-9a-f]+)?(?: fp=.*)?$`)
	createdBy   = regexp.MustCompile(`^created by (\S+?)(?: in goroutine \d+)?$`)
	minutes     = regexp.MustCompile(`^(\d+) minutes$`)
)

// Parse reads a dump from text. Lines that aren't part of one, such as log
// lines around it, are skipped.
func Parse(text string) (*Dump, error) {
	result := &Dump{}
	var current *Goroutine
	var pending *Frame
	inCreator := false
	leakProfile := false

	finish := func() {
		if current == nil {
			return
		}
		if pending != nil && current.Count > 0 {
			current.Frames = append(current.Frames, *pending)
			if !inCreator {
				result.CutOff = true
			}
		}
		pending = nil
		inCreator = false
		if current.ID != 0 || result.Format == FormatProfile {
			result.Goroutines = append(result.Goroutines, *current)
		}
		current = nil
	}

	for _, raw := range strings.Split(text, "\n") {
		line := timestamp.ReplaceAllString(strings.TrimRight(raw, "\r"), "")
		trimmed := strings.TrimSpace(line)
		if trimmed == "" {
			if result.Format == FormatProfile {
				finish()
			}
			continue
		}
		if match := profileHead.FindStringSubmatch(trimmed); match != nil {
			finish()
			result.Format = FormatProfile
			leakProfile = match[1] == "goroutineleak"
			continue
		}
		if match := profileRow.FindStringSubmatch(trimmed); match != nil && result.Format == FormatProfile {
			finish()
			count, err := strconv.Atoi(match[1])
			if err != nil {
				continue
			}
			current = &Goroutine{Count: count, Leaked: leakProfile}
			continue
		}
		if result.Format == FormatProfile {
			if current != nil {
				if match := profileLine.FindStringSubmatch(trimmed); match != nil {
					number, _ := strconv.Atoi(match[3])
					current.Frames = append(current.Frames, Frame{Func: match[1], File: match[2], Line: number})
				}
			}
			continue
		}
		if match := header.FindStringSubmatch(trimmed); match != nil {
			finish()
			result.Format = FormatTraceback
			id, err := strconv.Atoi(match[1])
			if err != nil {
				continue
			}
			current = &Goroutine{ID: id, Count: 1}
			readStatus(current, match[2])
			continue
		}
		if fatal, signal := readFatal(trimmed); fatal != "" || signal != "" {
			if result.Fatal == "" {
				result.Fatal = fatal
			}
			if result.Signal == "" {
				result.Signal = signal
			}
			continue
		}
		if trimmed == "runtime stack:" {
			finish()
			continue
		}
		if current == nil {
			continue
		}
		if pending != nil || inCreator {
			if match := fileLine.FindStringSubmatch(trimmed); match != nil {
				number, _ := strconv.Atoi(match[2])
				if inCreator {
					current.CreatedAt = match[1] + ":" + match[2]
					inCreator = false
				} else {
					pending.File, pending.Line = match[1], number
					current.Frames = append(current.Frames, *pending)
					pending = nil
				}
				continue
			}
		}
		if match := createdBy.FindStringSubmatch(trimmed); match != nil {
			current.CreatedBy = match[1]
			inCreator = true
			continue
		}
		if strings.HasPrefix(trimmed, "...") && strings.Contains(trimmed, "elided") {
			current.Elided = true
			continue
		}
		if strings.HasPrefix(trimmed, "[originating from goroutine") {
			// With tracebackancestors, what follows is the parent's stack at creation.
			finish()
			continue
		}
		if name, args, ok := readCall(trimmed); ok {
			if pending != nil {
				current.Frames = append(current.Frames, *pending)
			}
			pending = &Frame{Func: name, Args: args}
		}
	}
	finish()
	if len(result.Goroutines) == 0 {
		return nil, errors.New("found no goroutines. Expected Go's traceback or the output of /debug/pprof/goroutine?debug=1 or debug=2")
	}
	return result, nil
}

func readStatus(g *Goroutine, status string) {
	for index, part := range strings.Split(status, ", ") {
		if index == 0 {
			if strings.Contains(part, " (leaked)") {
				g.Leaked = true
			}
			part = strings.ReplaceAll(part, " (leaked)", "")
			part = strings.ReplaceAll(part, " (scan)", "")
			g.State = part
			continue
		}
		if part == "locked to thread" {
			g.Locked = true
		} else if match := minutes.FindStringSubmatch(part); match != nil {
			g.WaitMinutes, _ = strconv.Atoi(match[1])
		}
	}
}

func readFatal(line string) (fatal, signal string) {
	switch {
	case strings.HasPrefix(line, "fatal error: "):
		return line, ""
	case strings.HasPrefix(line, "panic: "):
		return line, ""
	case strings.HasPrefix(line, "SIGQUIT: "), strings.HasPrefix(line, "SIGABRT: "), strings.HasPrefix(line, "SIGSEGV: "), strings.HasPrefix(line, "SIGBUS: "):
		return "", line
	}
	return "", ""
}

// readCall splits "pkg.(*T).Method[...](0x1, 0x2)" into its name and arguments.
func readCall(line string) (name, args string, ok bool) {
	if !strings.HasSuffix(line, ")") {
		return "", "", false
	}
	depth := 0
	for index := len(line) - 1; index >= 0; index-- {
		switch line[index] {
		case ')':
			depth++
		case '(':
			depth--
			if depth == 0 {
				name = line[:index]
				if name == "" || strings.ContainsAny(name, " \t") {
					return "", "", false
				}
				return name, line[index+1 : len(line)-1], true
			}
		}
	}
	return "", "", false
}
