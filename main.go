package main

import (
	"fmt"
	"strings"

	"github.com/alexander-bachmann/errhance/internal/fmtstr"
)

func collectFmtDirectives(str string) (string, error) {
	ops, err := fmtstr.Parse(str, -1)
	if err != nil {
		return "", err
	}
	var directives []string
	for _, op := range ops {
		if op == nil {
			continue
		}
		// %% isn't a directive, it's an escape to print %
		if op.Text == "%%" {
			continue
		}
		directives = append(directives, op.Text)
	}
	return strings.Join(directives, ": "), nil
}

func main() {
	wtf := "%[1]*.[2]*[3]f | %+010d | %#x | %-15s | %#[4]o | %[5]T | %[6]q | %+[7]v | %08b | %#08X | %%"

	fmt.Println(collectFmtDirectives(wtf))

	// cmd.Execute()
}
