package main

import (
	"bytes"
	"encoding/json"
	"os"
	"strings"
	"testing"
)

const sample = "examples/dumps/mutex-late.txt"

func TestReview(t *testing.T) {
	var out, errs bytes.Buffer
	if code := run([]string{"review", sample}, nil, &out, &errs); code != 0 {
		t.Fatalf("exit %d: %s", code, errs.String())
	}
	for _, want := range []string{"Groups:", "SG107", "main.(*store).Refresh", "leads from one moment"} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("missing %q in:\n%s", want, out.String())
		}
	}
}

func TestHealthyReviewSaysSo(t *testing.T) {
	var out, errs bytes.Buffer
	if code := run([]string{"review", "examples/dumps/healthy-early.txt", "--fail-on", "low"}, nil, &out, &errs); code != 0 || !strings.Contains(out.String(), "Nothing here looks stuck") {
		t.Fatalf("exit %d: %s", code, out.String())
	}
}

func TestFailOnAndJSON(t *testing.T) {
	var out, errs bytes.Buffer
	if code := run([]string{"review", sample, "--fail-on", "high", "--json"}, nil, &out, &errs); code != 1 {
		t.Fatalf("exit %d", code)
	}
	var parsed map[string]any
	if err := json.Unmarshal(out.Bytes(), &parsed); err != nil {
		t.Fatal(err)
	}
	if parsed["total"] == nil {
		t.Errorf("%v", parsed)
	}
}

func TestStdinAndCompare(t *testing.T) {
	data, err := os.ReadFile(sample)
	if err != nil {
		t.Fatal(err)
	}
	var out, errs bytes.Buffer
	if code := run([]string{"review", "-"}, bytes.NewReader(data), &out, &errs); code != 0 {
		t.Fatalf("exit %d: %s", code, errs.String())
	}
	out.Reset()
	if code := run([]string{"compare", "examples/dumps/leak-early.txt", "examples/dumps/leak-late.txt"}, nil, &out, &errs); code != 0 {
		t.Fatalf("exit %d", code)
	}
	for _, want := range []string{"Growing:", "Same goroutine IDs", "Still there:"} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("missing %q in:\n%s", want, out.String())
		}
	}
}

func TestErrorsExitTwo(t *testing.T) {
	for _, args := range [][]string{{}, {"nope"}, {"review"}, {"review", "/no/such"}, {"review", "go.mod"}, {"compare", sample}, {"compare", "-", "-"}, {"review", sample, "--fail-on", "x"}} {
		var out, errs bytes.Buffer
		if code := run(args, strings.NewReader(""), &out, &errs); code != 2 {
			t.Errorf("%v: exit %d", args, code)
		}
	}
}

func TestVersion(t *testing.T) {
	var out, errs bytes.Buffer
	if code := run([]string{"version"}, nil, &out, &errs); code != 0 || strings.TrimSpace(out.String()) != version {
		t.Fatal(out.String())
	}
}
