package cmd

import (
	"bytes"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/alexander-bachmann/errhance/internal/errhance"
)

func FuzzWriteDiff(f *testing.F) {
	for _, seed := range [][3]string{
		{"", "", "empty.go"}, {"same", "same", "same.go"},
		{"old", "new\n", "a b.go"}, {"old\n", "new", "\"\t\n.go"},
		{"old\nsame", "new\nsame", "nested/file.go"},
		{"", "new", "new.go"}, {"old", "", "deleted.go"},
		{"a\n1\n2\n3\n4\n5\n6\n7\nz\n", "b\n1\n2\n3\n4\n5\n6\n7\ny\n", "multi.go"},
	} {
		f.Add(seed[0], seed[1], seed[2])
	}
	f.Fuzz(func(t *testing.T, before, after, path string) {
		if len(before) > 2048 || len(after) > 2048 || len(path) > 128 {
			t.Skip()
		}
		// Paths are labels only: no filesystem access or per-iteration temp dirs.
		changes := []errhance.FileChange{{Path: filepath.Join("fixture", path), Before: before, After: after}}
		var first, second bytes.Buffer
		if err := writeDiff(&first, ".", changes); err != nil {
			t.Fatal(err)
		}
		if err := writeDiff(&second, ".", changes); err != nil {
			t.Fatal(err)
		}
		if first.String() != second.String() {
			t.Fatal("nondeterministic diff")
		}
		if before == after {
			if first.Len() != 0 {
				t.Fatal("nonempty no-op diff")
			}
			return
		}
		if got := fuzzApplyDiff(t, before, first.String()); got != after {
			t.Fatalf("reconstruction mismatch: got %q, want %q\n%s", got, after, first.String())
		}
	})
}

// Decode only the unified format emitted by writeDiff, not arbitrary patches.
func fuzzApplyDiff(t *testing.T, before, patch string) string {
	t.Helper()
	lines := strings.SplitAfter(patch, "\n")
	if len(lines) < 4 || !strings.HasPrefix(lines[0], "--- ") || !strings.HasPrefix(lines[1], "+++ ") || lines[len(lines)-1] != "" {
		t.Fatalf("invalid diff headers: %q", patch)
	}
	lines = lines[2 : len(lines)-1]
	old := strings.SplitAfter(before, "\n")
	if old[len(old)-1] == "" {
		old = old[:len(old)-1]
	}
	parseRange := func(s string) (int, int) {
		parts := strings.Split(s, ",")
		start, err := strconv.Atoi(parts[0])
		if err != nil || start < 0 || len(parts) > 2 {
			t.Fatalf("invalid hunk range %q", s)
		}
		count := 1
		if len(parts) == 2 {
			count, err = strconv.Atoi(parts[1])
			if err != nil || count < 0 {
				t.Fatalf("invalid hunk count %q", s)
			}
		}
		if count != 0 {
			start--
		}
		return start, count
	}
	var out strings.Builder
	pos, written := 0, 0
	for i := 0; i < len(lines); {
		header := strings.Fields(lines[i])
		if len(header) != 4 || header[0] != "@@" || header[3] != "@@" || !strings.HasPrefix(header[1], "-") || !strings.HasPrefix(header[2], "+") {
			t.Fatalf("invalid hunk header: %q", lines[i])
		}
		start, oldCount := parseRange(header[1][1:])
		newStart, newCount := parseRange(header[2][1:])
		if start < pos || start > len(old) {
			t.Fatal("hunk outside source")
		}
		for ; pos < start; pos++ {
			out.WriteString(old[pos])
			written++
		}
		if newStart != written {
			t.Fatal("incorrect new hunk position")
		}
		i++
		removed, added := 0, 0
		for i < len(lines) && !strings.HasPrefix(lines[i], "@@ ") {
			line := lines[i]
			i++
			if len(line) < 2 || !strings.ContainsRune(" +-", rune(line[0])) {
				t.Fatalf("invalid hunk line %q", line)
			}
			text := line[1:]
			if i < len(lines) && lines[i] == "\\ No newline at end of file\n" {
				text = strings.TrimSuffix(text, "\n")
				i++
			}
			if line[0] != '+' {
				if pos >= len(old) || old[pos] != text {
					t.Fatalf("context/deletion mismatch at line %d", pos)
				}
				pos++
				removed++
			}
			if line[0] != '-' {
				out.WriteString(text)
				written++
				added++
			}
		}
		if removed != oldCount || added != newCount {
			t.Fatalf("hunk counts: got %d/%d, want %d/%d", removed, added, oldCount, newCount)
		}
	}
	for ; pos < len(old); pos++ {
		out.WriteString(old[pos])
	}
	return out.String()
}
