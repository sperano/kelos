package logview

import (
	"bytes"
	"flag"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/muesli/termenv"
)

var update = flag.Bool("update", false, "rewrite the golden files in testdata")

// TestRenderGolden renders each agent's fixture with and without -v and
// compares the uncolored output with testdata/<agent>[.verbose].golden.
func TestRenderGolden(t *testing.T) {
	agents := map[string]string{
		"claude":   "",
		"codex":    AgentCodex,
		"gemini":   AgentGemini,
		"opencode": AgentOpenCode,
	}
	for fixture, agentType := range agents {
		for _, verbose := range []bool{false, true} {
			name := fixture
			if verbose {
				name += ".verbose"
			}
			t.Run(name, func(t *testing.T) {
				got := renderFixture(t, fixture, agentType, Options{Verbose: verbose})
				assertGolden(t, filepath.Join("testdata", name+".golden"), got)
			})
		}
	}
}

func renderFixture(t *testing.T, fixture, agentType string, opts Options) string {
	t.Helper()
	input, err := os.Open(filepath.Join("testdata", fixture+".ndjson"))
	if err != nil {
		t.Fatal(err)
	}
	defer input.Close()

	var out bytes.Buffer
	renderer := NewRenderer(&out, opts)
	if err := Parse(agentType, input, renderer.Render); err != nil {
		t.Fatalf("Parse: %v", err)
	}
	renderer.Finish()
	return out.String()
}

func assertGolden(t *testing.T, path, got string) {
	t.Helper()
	if *update {
		if err := os.WriteFile(path, []byte(got), 0o644); err != nil {
			t.Fatal(err)
		}
		return
	}
	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("reading golden file (run go test -update to create it): %v", err)
	}
	if got != string(want) {
		t.Errorf("output differs from %s (run go test -update to accept):\n--- got ---\n%s\n--- want ---\n%s", path, got, want)
	}
}

func renderDiff(profile termenv.Profile, width int) string {
	var out bytes.Buffer
	renderer := newRendererWithProfile(&out, Options{Color: true, Width: width}, profile)
	renderer.Render(ToolCall{ID: "e", Name: "Update", Summary: "a.go", Diff: []Hunk{replacementHunk("x := 1", "\tx := 2")}})
	renderer.Render(ToolResult{ID: "e"})
	return out.String()
}

func TestRenderColorsDiffTextOnBasicTerminals(t *testing.T) {
	const (
		ansiGreen = "\x1b[32m"
		ansiRed   = "\x1b[31m"
	)
	got := renderDiff(termenv.ANSI, 0)
	for _, want := range []string{ansiGreen + "+     x := 2", ansiRed + "- x := 1"} {
		if !strings.Contains(got, want) {
			t.Errorf("colored output lacks %q:\n%q", want, got)
		}
	}
}

func TestRenderTintsDiffLinesToTerminalWidth(t *testing.T) {
	const (
		width          = 40
		trueColorTint  = "\x1b[48;2;"
		paddedAddition = "+     x := 2" + "                 "
	)
	got := renderDiff(termenv.TrueColor, width)
	if !strings.Contains(got, trueColorTint) {
		t.Errorf("output lacks a background tint:\n%q", got)
	}
	if !strings.Contains(got, paddedAddition) {
		t.Errorf("added line is not padded to %d columns:\n%q", width, got)
	}
}

func TestRenderWithoutColorHasNoEscapes(t *testing.T) {
	got := renderFixture(t, "claude", "", Options{})
	if strings.Contains(got, "\x1b[") {
		t.Errorf("uncolored output contains ANSI escapes:\n%s", got)
	}
}

func TestRenderRepeatsHeaderForOutOfOrderResult(t *testing.T) {
	var out bytes.Buffer
	renderer := NewRenderer(&out, Options{})
	renderer.Render(ToolCall{ID: "a", Name: "Read", Summary: "a.go"})
	renderer.Render(ToolCall{ID: "b", Name: "Read", Summary: "b.go"})
	renderer.Render(ToolResult{ID: "a", Output: "package a"})
	renderer.Render(ToolResult{ID: "b", Output: "package b"})

	want := strings.Join([]string{
		"⏺ Read(a.go)",
		"",
		"⏺ Read(b.go)",
		"",
		"⏺ Read(a.go)",
		"  ⎿  package a",
		"",
		"⏺ Read(b.go)",
		"  ⎿  package b",
		"",
	}, "\n")
	if got := out.String(); got != want {
		t.Errorf("got:\n%s\nwant:\n%s", got, want)
	}
}

func TestRenderStreamedTextContinuesOneLine(t *testing.T) {
	var out bytes.Buffer
	renderer := NewRenderer(&out, Options{})
	renderer.Render(Text{Text: "Hello, ", Delta: true})
	renderer.Render(Text{Text: "world.", Delta: true})
	renderer.Finish()

	if got, want := out.String(), "⏺ Hello, world.\n"; got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestHumanCount(t *testing.T) {
	tests := map[int]string{
		0:         "0",
		950:       "950",
		18_456:    "18.5k",
		2_374_433: "2.37M",
	}
	for n, want := range tests {
		if got := humanCount(n); got != want {
			t.Errorf("humanCount(%d) = %q, want %q", n, got, want)
		}
	}
}
