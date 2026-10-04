package cli

import (
	"fmt"
	"io"
	"os"

	"github.com/spf13/cobra"
	"golang.org/x/term"

	"github.com/kelos-dev/kelos/internal/logview"
)

// Values of the logs --color flag.
const (
	colorAuto   = "auto"
	colorAlways = "always"
	colorNever  = "never"
)

// logOutputFlags are the logs command flags that choose how agent output
// is printed.
type logOutputFlags struct {
	color   string
	verbose bool
	raw     bool
}

func (f *logOutputFlags) register(cmd *cobra.Command) {
	cmd.Flags().StringVar(&f.color, "color", colorAuto,
		"render agent output in color: auto (when stdout is a terminal and NO_COLOR is unset), always, or never (plain text)")
	cmd.Flags().BoolVarP(&f.verbose, "verbose", "v", false,
		"show full tool output, diffs, reasoning and sub-agent activity in color output")
	cmd.Flags().BoolVar(&f.raw, "raw", false, "print the agent's NDJSON output unparsed")
}

// logOutput is how agent output is printed once the flags are resolved.
type logOutput struct {
	raw     bool
	pretty  bool
	verbose bool
}

// resolve checks the flags and decides whether stdout gets color output.
func (f *logOutputFlags) resolve(stdout *os.File) (logOutput, error) {
	out := logOutput{raw: f.raw, verbose: f.verbose}
	switch f.color {
	case colorAlways:
		out.pretty = true
	case colorNever:
	case colorAuto:
		out.pretty = isColorTerminal(stdout)
	default:
		return logOutput{}, fmt.Errorf("invalid --color %q: must be %s, %s or %s", f.color, colorAuto, colorAlways, colorNever)
	}
	return out, nil
}

// isColorTerminal reports whether f is a terminal that accepts color, per
// the NO_COLOR convention (https://no-color.org).
func isColorTerminal(f *os.File) bool {
	if os.Getenv("NO_COLOR") != "" || os.Getenv("TERM") == "dumb" {
		return false
	}
	return term.IsTerminal(int(f.Fd()))
}

// write prints agentType's log stream to stdout, and to stderr for the
// status lines of the plain format.
func (o logOutput) write(agentType string, stream io.Reader) error {
	switch {
	case o.raw:
		if _, err := io.Copy(os.Stdout, stream); err != nil {
			return fmt.Errorf("reading logs: %w", err)
		}
		return nil
	case o.pretty:
		renderer := logview.NewRenderer(os.Stdout, logview.Options{Color: true, Verbose: o.verbose})
		defer renderer.Finish()
		return logview.Parse(agentType, stream, renderer.Render)
	default:
		return parsePlainAgentLogs(agentType, stream)
	}
}

// parsePlainAgentLogs prints agent output in the plain format: assistant
// text on stdout, status and tool lines on stderr.
func parsePlainAgentLogs(agentType string, stream io.Reader) error {
	switch agentType {
	case logview.AgentCodex:
		return ParseAndFormatCodexLogs(stream, os.Stdout, os.Stderr)
	case logview.AgentGemini:
		return ParseAndFormatGeminiLogs(stream, os.Stdout, os.Stderr)
	case logview.AgentOpenCode:
		return ParseAndFormatOpenCodeLogs(stream, os.Stdout, os.Stderr)
	default:
		return ParseAndFormatLogs(stream, os.Stdout, os.Stderr)
	}
}
