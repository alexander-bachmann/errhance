package cmd

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"

	"github.com/alexander-bachmann/errhance/internal/errhance"
	"github.com/alexander-bachmann/errhance/internal/fastwalk"
)

func errhanceFiles(cfg fastwalk.Config) error {
	valid, err := inGoProject()
	if err != nil || !valid {
		return fmt.Errorf("not currently in Go project: %w", err) // noeh
	}
	err = downloadGoimportsIfMissing()
	if err != nil {
		return fmt.Errorf("failed to download goimports: %w", err) // noeh
	}
	err = fastwalk.Walk(cfg)
	if err != nil {
		return fmt.Errorf("failed while walking file system: %w", err) // noeh
	}
	return nil
}

func errhanceFile(path string, cfg errhance.Config) (int, error) {
	before, err := os.ReadFile(path)
	if err != nil {
		return 0, fmt.Errorf("os.ReadFile: %w", err)
	}
	after, numErrsEnhanced, err := errhance.File(string(before), cfg)
	if err != nil {
		return numErrsEnhanced, fmt.Errorf("errhance.File: %w", err)
	}
	// len check first for performance
	if len(before) == len(after) && string(before) == after {
		return numErrsEnhanced, nil // no changes made
	}
	// open the file for writing and truncate
	// its content without modifying permissions
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_TRUNC, 0)
	if err != nil {
		return numErrsEnhanced, fmt.Errorf("os.OpenFile: %w", err)
	}
	defer file.Close()
	if _, err := file.Write([]byte(after)); err != nil {
		return numErrsEnhanced, fmt.Errorf("file.Write: %w", err)
	}
	if err := file.Sync(); err != nil {
		return numErrsEnhanced, fmt.Errorf("file.Sync: %w", err)
	}
	err = runGoimportsOnFile(path)
	if err != nil {
		return numErrsEnhanced, fmt.Errorf("failed to run goimports on %s: %w", path, err) // noeh
	}
	return numErrsEnhanced, nil
}

func inGoProject() (bool, error) {
	cwd, err := os.Getwd()
	if err != nil {
		return false, fmt.Errorf("os.Getwd: %w", err)
	}
	for {
		goModPath := filepath.Join(cwd, "go.mod")
		if _, err := os.Stat(goModPath); err != nil {
			if os.IsNotExist(err) {
				// go.mod not found, check the parent directory.
			} else {
				return false, fmt.Errorf("os.Stat: %s: %w", goModPath, err) // noerrhance
			}
		} else {
			return true, nil
		}
		parent := filepath.Dir(cwd)
		if parent == cwd { // reached the root
			break
		}
		cwd = parent
	}
	return false, nil
}

func downloadGoimportsIfMissing() error {
	if _, err := exec.LookPath("goimports"); err == nil {
		return nil
	}
	cmd := exec.Command("go", "install", "golang.org/x/tools/cmd/goimports@latest")
	output, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("%w %s", err, output) // noeh
	}
	return nil
}

func runGoimportsOnFile(path string) error {
	cmd := exec.Command("goimports", "-w", path)
	output, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("%w %s", err, output) // noeh
	}
	return nil
}
