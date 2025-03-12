package main

import (
	"context"
	"fmt"
	"os"

	"github.com/alexander-bachmann/errhance/internal/fastwalk"
)

func main() {
	// cmd.Execute()

	errhanceFunc := func(ctx context.Context, path string) error {
		b, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		fmt.Println(path, ":", len(b))
		return nil
	}

	err := fastwalk.Walk(
		fastwalk.NewConfig(
			errhanceFunc,
			fastwalk.WithOnlyExtensions([]string{".go"}),
		))
	if err != nil {
		panic(err)
	}

}
