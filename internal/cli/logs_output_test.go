package cli

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLogOutputFlagsResolve(t *testing.T) {
	notTerminal, err := os.Create(filepath.Join(t.TempDir(), "out"))
	if err != nil {
		t.Fatal(err)
	}
	defer notTerminal.Close()

	tests := []struct {
		name       string
		flags      logOutputFlags
		wantPretty bool
		wantErr    bool
	}{
		{name: "always", flags: logOutputFlags{color: colorAlways}, wantPretty: true},
		{name: "never", flags: logOutputFlags{color: colorNever}},
		{name: "auto without a terminal", flags: logOutputFlags{color: colorAuto}},
		{name: "invalid", flags: logOutputFlags{color: "sometimes"}, wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			out, err := tt.flags.resolve(notTerminal)
			if (err != nil) != tt.wantErr {
				t.Fatalf("resolve() error = %v, wantErr %v", err, tt.wantErr)
			}
			if out.pretty != tt.wantPretty {
				t.Errorf("pretty = %v, want %v", out.pretty, tt.wantPretty)
			}
		})
	}
}

func TestIsColorTerminalHonorsNoColor(t *testing.T) {
	t.Setenv("NO_COLOR", "1")
	if isColorTerminal(os.Stdout) {
		t.Error("isColorTerminal() = true with NO_COLOR set")
	}
}

func TestLogOutputFlagsKeepRawAndVerbose(t *testing.T) {
	flags := logOutputFlags{color: colorNever, raw: true, verbose: true}
	out, err := flags.resolve(os.Stdout)
	if err != nil {
		t.Fatal(err)
	}
	if !out.raw || !out.verbose {
		t.Errorf("got %+v, want raw and verbose set", out)
	}
}
