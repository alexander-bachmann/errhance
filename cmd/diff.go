package cmd

import (
	"fmt"
	"io"
	"path/filepath"
	"sort"
	"strings"

	"github.com/pmezard/go-difflib/difflib"

	"github.com/alexander-bachmann/errhance/internal/errhance"
)

func writeDiff(out io.Writer, root string, changes []errhance.FileChange) error {
	changes = append([]errhance.FileChange(nil), changes...)
	sort.Slice(changes, func(i, j int) bool { return changes[i].Path < changes[j].Path })
	for _, change := range changes {
		path, err := filepath.Rel(root, change.Path)
		if err != nil {
			return fmt.Errorf("diff path: %w", err)
		}
		path = filepath.ToSlash(path)
		patch, err := difflib.GetUnifiedDiffString(difflib.UnifiedDiff{
			A: diffLines(change.Before), B: diffLines(change.After),
			FromFile: diffPath("a/" + path), ToFile: diffPath("b/" + path),
			Context: 3,
		})
		if err != nil {
			return fmt.Errorf("unified diff: %w", err)
		}
		// WriteUnifiedDiff ignores its final buffered flush error. Build in memory
		// first, then write directly so even small-output failures reach callers.
		if patch != "" {
			if _, err := io.WriteString(out, patch); err != nil {
				return fmt.Errorf("write diff: %w", err)
			}
		}
	}
	return nil
}

func diffLines(text string) []string {
	if text == "" {
		return nil
	}
	lines := strings.SplitAfter(text, "\n")
	last := len(lines) - 1
	if lines[last] == "" {
		return lines[:last]
	}
	// Keep the EOF marker attached to its logical line: difflib must count
	// only source lines, and must distinguish terminated from unterminated EOF.
	lines[last] += "\n\\ No newline at end of file\n"
	return lines
}

func diffPath(path string) string {
	if !strings.ContainsAny(path, " \"\\") && strings.IndexFunc(path, func(r rune) bool { return r < 0x20 || r >= 0x7f }) < 0 {
		return path
	}
	var quoted strings.Builder
	quoted.WriteByte('"')
	for i := 0; i < len(path); i++ {
		switch c := path[i]; c {
		case '"', '\\':
			quoted.WriteByte('\\')
			quoted.WriteByte(c)
		case '\n':
			quoted.WriteString(`\n`)
		case '\r':
			quoted.WriteString(`\r`)
		case '\t':
			quoted.WriteString(`\t`)
		default:
			if c < 0x20 || c >= 0x7f {
				// Git path quoting uses octal bytes, not Go's hex/Unicode escapes.
				fmt.Fprintf(&quoted, "\\%03o", c)
			} else {
				quoted.WriteByte(c)
			}
		}
	}
	quoted.WriteByte('"')
	return quoted.String()
}
