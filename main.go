package main

import (
	"fmt"
	"strings"

	"github.com/alexander-bachmann/errhance/internal/fmtstr"
)

func parseDirectives(str string) ([]string, error) {
	ops, err := fmtstr.Parse(str, -1)
	if err != nil {
		return nil, err
	}
	var dirs []string
	for _, op := range ops {
		if op == nil {
			continue
		}
		// %% isn't a directive, it's an escape to print %
		if op.Text == "%%" {
			continue
		}
		dirs = append(dirs, op.Text)
	}
	return dirs, nil
}

// if more directives than args, abort? error and print why?

// e.g. "io.ReadAll", ["%w", "%s"] => "io.ReadAll: %w: %s"
func newFmtErrorfMsg(fnName string, dirs []string) string {
	if len(dirs) == 0 {
		dirs = []string{"%w"}
	}
	return fmt.Sprintf(`"%s: %s"`, fnName, strings.Join(dirs, ": "))
}

// e.g. "io.ReadAll: %w: %s", ["err", "url"] => fmt.Errorf("io.ReadAll: %w: %s", err, url)
func newFmtErrorf(msg string, args []string) string {
	if len(args) == 0 {
		args = []string{"err"}
	}
	return fmt.Sprintf(`fmt.Errorf(%s, %s)`, msg, strings.Join(args, ", "))
}

func main() {
	// wtf := "%[1]*.[2]*[3]f | %+010d | %#x | %-15s | %#[4]o | %[5]T | %[6]q | %+[7]v | %08b | %#08X | %%"

	// if more than just err but only 1 formatting directive, default to %v?

	fnName := "io.ReadAll"
	args := []string{"err", "url", "rawBody"}
	someErr := `fmt.Errorf("failed to read body: %w | url: %s | body: %s", err, url, rawBody)`

	dirs, err := parseDirectives(someErr)
	if err != nil {
		panic(err)
	}
	msg := newFmtErrorfMsg(fnName, dirs)

	er := newFmtErrorf(msg, args)
	fmt.Printf("er: %v\n", er)

	// cmd.Execute()
}
