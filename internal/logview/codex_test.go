package logview

import "testing"

func TestUnwrapCodexCommand(t *testing.T) {
	tests := []struct {
		name    string
		command string
		want    string
	}{
		{"single quoted", `/bin/bash -lc 'git status --short'`, "git status --short"},
		{"double quoted", `/bin/bash -lc "sed -n '1,20p' main.go"`, "sed -n '1,20p' main.go"},
		{"mixed quoting", `/bin/bash -lc "rg -g '"'!vendor'"' x"`, "rg -g '!vendor' x"},
		{"escaped single quote", `/bin/bash -lc 'echo '"'"'hi'"'"''`, "echo 'hi'"},
		{"backslash kept in double quotes", `/bin/bash -lc "printf 'a\n'"`, `printf 'a\n'`},
		{"escaped dollar", `/bin/bash -lc "echo \$HOME"`, "echo $HOME"},
		{"not wrapped", "make test", "make test"},
		{"several words left alone", `/bin/bash -lc 'a' 'b'`, `/bin/bash -lc 'a' 'b'`},
		{"unterminated quote left alone", `/bin/bash -lc 'a`, `/bin/bash -lc 'a`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := unwrapCodexCommand(tt.command); got != tt.want {
				t.Errorf("unwrapCodexCommand(%q) = %q, want %q", tt.command, got, tt.want)
			}
		})
	}
}

func TestCodexCommandResultMarksNonZeroExit(t *testing.T) {
	exitCode := 2
	result := codexCommandResult(&codexItem{ID: "i", AggregatedOutput: "boom\n", ExitCode: &exitCode, Status: "failed"})
	if !result.IsError || result.Output != "exit code 2\nboom" {
		t.Errorf("got %+v, want an error with the exit code and output", result)
	}
}
