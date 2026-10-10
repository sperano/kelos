package logview

import (
	"bytes"
	"strings"
	"testing"

	"github.com/muesli/termenv"
)

func TestCleanRemovesControlCharacters(t *testing.T) {
	tests := map[string]string{
		"title \x1b]0;pwned\x07 done": "title ]0;pwned done",
		"line\r\nnext":                "line\nnext",
		"tab\tkept":                   "tab\tkept",
		"c1 \u009b31m":                "c1 31m",
		"del \x7f":                    "del ",
	}
	for input, want := range tests {
		if got := clean(input); got != want {
			t.Errorf("clean(%q) = %q, want %q", input, got, want)
		}
	}
}

func TestRenderStripsEscapesFromToolOutput(t *testing.T) {
	var out bytes.Buffer
	renderer := newRendererWithProfile(&out, Options{Color: true}, termenv.TrueColor)
	renderer.Render(ToolCall{ID: "a", Name: "Bash", Summary: "cat \x1b[2Jevil"})
	renderer.Render(ToolResult{ID: "a", Output: "\x1b]0;title\x07\rhidden"})

	for _, forbidden := range []string{"\x1b]", "\x1b[2J", "\x07", "\r"} {
		if strings.Contains(out.String(), forbidden) {
			t.Errorf("output contains %q:\n%q", forbidden, out.String())
		}
	}
}

func TestRenderResultWithoutCall(t *testing.T) {
	var out bytes.Buffer
	renderer := NewRenderer(&out, Options{})
	renderer.Render(ToolResult{ID: "unknown", Output: "done"})

	if got, want := out.String(), "⏺ Tool result\n  ⎿  done\n"; got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}
