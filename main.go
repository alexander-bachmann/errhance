package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/alexander-bachmann/errhance/pkg/errhance"
)

func main() {
	err := cli()
	if err != nil {
		fmt.Println(err)
		return
	}
	// err := testing()
	// if err != nil {
	// 	fmt.Println(err)
	// 	return
	// }
}

func cli() error {
	currentDir, err := os.Getwd()
	if err != nil {
		return fmt.Errorf("os.Getwd: %w", err)
	}
	if !isGitDirectory(currentDir) {
		return fmt.Errorf("not at the root of a git directory; not risking it")
	}
	err = filepath.Walk(currentDir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return fmt.Errorf("filepath.Walk: %w", err)
		}
		if info.IsDir() ||
			!strings.HasSuffix(info.Name(), ".go") ||
			strings.HasSuffix(info.Name(), "_test.go") ||
			// skip generated code
			strings.HasSuffix(info.Name(), ".sql.go") ||
			strings.HasSuffix(info.Name(), "_gen.go") ||
			strings.HasSuffix(info.Name(), "_mock.go") ||
			strings.HasSuffix(info.Name(), "_pb.go") ||
			strings.HasSuffix(info.Name(), ".pb.go") ||
			strings.HasSuffix(info.Name(), "_stringer.go") {
			return nil
		}
		err = processFile(path)
		if err != nil {
			return fmt.Errorf("processFile: %w", err)
		}
		return nil
	})
	if err != nil {
		return fmt.Errorf("filepath.Walk: %w", err)
	}
	return nil
}

func isGitDirectory(dir string) bool {
	gitDir := filepath.Join(dir, ".git")
	if _, err := os.Stat(gitDir); os.IsNotExist(err) {
		return false
	}
	return true
}

func processFile(path string) error {
	src, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("os.ReadFile: %w", err)
	}
	newSrc, err := errhance.Do(errhance.Config{}, string(src))
	if err != nil {
		return fmt.Errorf("errhance.Do: %w", err)
	}
	err = os.WriteFile(path, []byte(newSrc), 0644)
	if err != nil {
		return fmt.Errorf("os.WriteFile: %w", err)
	}
	return nil
}

func testing() error {
	src := `package main
	import (
		"foo"
		"fee/fi/fo"
		"fiddly"
	)
	func A() error {
		b := foo.Bar{}
		err := b.Baz()
		if err != nil {
			return err
		}
		err = fo.Fum()
		if err != nil {
			return err
		}
		err = meep()
		if err != nil {
			return err
		}
		err = fiddly.Widdly().Weddly().Woddly()
		if err != nil {
			return err
		}
		err = a().b().c().d()
		if err != nil {
			return err
		}
	}`
	fmt.Println(src, "\n---")
	src, err := errhance.Do(errhance.Config{}, src)
	if err != nil {
		return fmt.Errorf("errhance.Do: %w", err)
	}
	fmt.Println(src)
	return nil
}
