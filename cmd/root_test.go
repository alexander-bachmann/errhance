package cmd

import "testing"

func TestRejectPositionalTargets(t *testing.T) {
	if err := rootCmd.ValidateArgs([]string{"one.go"}); err == nil {
		t.Fatal("positional targets must not silently select the current directory")
	}
	if err := rootCmd.ValidateArgs(nil); err != nil {
		t.Fatal(err)
	}
}
