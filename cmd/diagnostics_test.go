package cmd

import (
	"bytes"
	"errors"
	"go/token"
	"io"
	"strings"
	"testing"

	"github.com/alexander-bachmann/errhance/internal/errhance"
)

func TestWriteDiagnostics(t *testing.T) {
	diagnostics := []errhance.Diagnostic{
		{Message: "zebra", Position: token.Position{Filename: "b.go", Line: 1, Column: 3}},
		{Message: "alpha", Position: token.Position{Filename: "a.go", Line: 10, Column: 4}},
		{Message: "alpha", Position: token.Position{Filename: "a.go", Line: 2, Column: 8}},
		{Message: "alpha"},
		{Message: "file only", Position: token.Position{Filename: "excluded.go"}},
		{Message: "unknown"},
	}
	grouped := "6 diagnostics (4 reasons):\n" +
		"  3 x alpha (first: a.go:2)\n" +
		"  1 x file only (first: excluded.go)\n" +
		"  1 x unknown (first: <unknown>)\n" +
		"  1 x zebra (first: b.go:1)\n"
	verbose := "b.go:1:3: zebra\na.go:10:4: alpha\na.go:2:8: alpha\nalpha\nexcluded.go: file only\nunknown\n"
	for _, tc := range []struct {
		name        string
		diagnostics []errhance.Diagnostic
		verbose     bool
		want        string
	}{
		{"grouped", diagnostics, false, grouped},
		{"verbose", diagnostics, true, verbose},
		{"small lists grouped", diagnostics[:1], false, "1 diagnostics (1 reasons):\n  1 x zebra (first: b.go:1)\n"},
		{"empty", nil, false, ""},
		{"empty verbose", nil, true, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var out bytes.Buffer
			if err := writeDiagnostics(&out, tc.diagnostics, tc.verbose); err != nil {
				t.Fatal(err)
			}
			if out.String() != tc.want {
				t.Fatalf("output=%q, want %q", out.String(), tc.want)
			}
		})
	}
	for i, j := 0, len(diagnostics)-1; i < j; i, j = i+1, j-1 {
		diagnostics[i], diagnostics[j] = diagnostics[j], diagnostics[i]
	}
	var out bytes.Buffer
	if err := writeDiagnostics(&out, diagnostics, false); err != nil {
		t.Fatal(err)
	}
	if out.String() != grouped {
		t.Fatalf("reordered output=%q, want %q", out.String(), grouped)
	}
}

type diagnosticFailWriter struct {
	writesLeft int
}

func (w *diagnosticFailWriter) Write(p []byte) (int, error) {
	if w.writesLeft == 0 {
		return 0, errOutput
	}
	w.writesLeft--
	return len(p), nil
}

func TestWriteDiagnosticsFailure(t *testing.T) {
	diagnostics := []errhance.Diagnostic{{Message: "one"}, {Message: "two"}}
	for _, verbose := range []bool{false, true} {
		writes := 3
		if verbose {
			writes = 2
		}
		for n := 0; n < writes; n++ {
			if err := writeDiagnostics(&diagnosticFailWriter{writesLeft: n}, diagnostics, verbose); !errors.Is(err, errOutput) {
				t.Fatalf("verbose=%v, writes before failure=%d: error=%v", verbose, n, err)
			}
		}
	}
}

func TestCLIDiagnostics(t *testing.T) {
	cliProject(t, cliSource+"\nfunc Loop() error { for { return source() } }\n")
	for _, verbose := range []bool{false, true} {
		cmd := newRootCmd()
		args := []string{"--diff"}
		if verbose {
			args = append(args, "--verbose")
		}
		cmd.SetArgs(args)
		var stdout, stderr bytes.Buffer
		cmd.SetOut(&stdout)
		cmd.SetErr(&stderr)
		if err := cmd.Execute(); err != nil {
			t.Fatal(err)
		}
		if !strings.HasPrefix(stdout.String(), "--- a/foo.go\n+++ b/foo.go\n") || strings.Contains(stdout.String(), "diagnostics") || strings.Contains(stdout.String(), "control flow") {
			t.Fatalf("unexpected stdout: %s", &stdout)
		}
		if !strings.Contains(stderr.String(), "foo.go:") || strings.Contains(stderr.String(), "diagnostics (") == verbose {
			t.Fatalf("verbose=%v, unexpected stderr: %s", verbose, &stderr)
		}

		// In dry-run mode only diagnostics use stderr, isolating their write errors.
		cmd = newRootCmd()
		args[0] = "--dry-run"
		cmd.SetArgs(args)
		cmd.SetOut(io.Discard)
		cmd.SetErr(failedWriter{})
		cmd.SilenceErrors, cmd.SilenceUsage = true, true
		if err := cmd.Execute(); !errors.Is(err, errOutput) {
			t.Fatalf("verbose=%v: error=%v, want output error", verbose, err)
		}
	}
}
