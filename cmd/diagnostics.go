package cmd

import (
	"fmt"
	"go/token"
	"io"
	"sort"

	"github.com/alexander-bachmann/errhance/internal/errhance"
)

func writeDiagnostics(w io.Writer, diagnostics []errhance.Diagnostic, verbose bool) error {
	if verbose {
		for _, diagnostic := range diagnostics {
			var err error
			if diagnostic.Position.IsValid() || diagnostic.Position.Filename != "" {
				_, err = fmt.Fprintf(w, "%s: %s\n", diagnostic.Position, diagnostic.Message)
			} else {
				_, err = fmt.Fprintln(w, diagnostic.Message)
			}
			if err != nil {
				return err
			}
		}
		return nil
	}
	if len(diagnostics) == 0 {
		return nil
	}
	// Sort a copy so the first location is stable without changing verbose output.
	ordered := append([]errhance.Diagnostic(nil), diagnostics...)
	sort.Slice(ordered, func(i, j int) bool {
		a, b := ordered[i].Position, ordered[j].Position
		if a.Filename != b.Filename {
			return a.Filename < b.Filename
		}
		if a.Line != b.Line {
			return a.Line < b.Line
		}
		return a.Column < b.Column
	})
	type reason struct {
		count int
		first token.Position
	}
	groups := make(map[string]reason)
	var messages []string
	for _, diagnostic := range ordered {
		group := groups[diagnostic.Message]
		if group.count == 0 {
			messages = append(messages, diagnostic.Message)
		}
		if group.count == 0 || (!group.first.IsValid() && group.first.Filename == "") {
			group.first = diagnostic.Position
		}
		group.count++
		groups[diagnostic.Message] = group
	}
	sort.Strings(messages)
	if _, err := fmt.Fprintf(w, "%d diagnostics (%d reasons):\n", len(diagnostics), len(messages)); err != nil {
		return err
	}
	for _, message := range messages {
		group := groups[message]
		location := group.first.Filename
		if location == "" {
			location = "<unknown>"
		}
		if group.first.IsValid() {
			location = fmt.Sprintf("%s:%d", location, group.first.Line)
		}
		if _, err := fmt.Fprintf(w, "  %d x %s (first: %s)\n", group.count, message, location); err != nil {
			return err
		}
	}
	return nil
}
