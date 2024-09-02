package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/alexander-bachmann/errhance/pkg/errhance"
)

func main() {
	currentDir, err := os.Getwd()
	if err != nil {
		fmt.Printf("os.Getwd: %v", err)
		return
	}
	if !isGitDirectory(currentDir) {
		fmt.Println("not at the root of a git directory; not risking it for you")
		return
	}
	err = filepath.Walk(currentDir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if info.IsDir() ||
			!strings.HasSuffix(info.Name(), ".go") ||
			strings.HasSuffix(info.Name(), "_test.go") {
			return nil
		}
		err = processFile(path)
		if err != nil {
			return fmt.Errorf("processFile: %w", err)
		}
		return nil
	})
	if err != nil {
		fmt.Printf("filepath.Walk: %v", err)
		return
	}
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
