package errhance

import (
	"context"
	"fmt"
	"go/token"
	"path/filepath"
	"sync"

	"github.com/alexander-bachmann/errhance/internal/fastwalk"
)

type DirectoryReport struct {
	TotalFilesModified  int
	TotalErrorsEnhanced int
	Diagnostics         []Diagnostic
}

func LocalDirectory(dirCfg DirectoryConfig, srcCfg SourceConfig) (DirectoryReport, error) {
	return LocalDirectoryContext(context.Background(), dirCfg, srcCfg)
}

// LocalDirectoryContext honors walker selection, then validates all loaded
// packages before writing. Counts describe committed files, including on error.
// Validation covers only the active build configuration, not all GOOS/tag sets.
func LocalDirectoryContext(ctx context.Context, dirCfg DirectoryConfig, srcCfg SourceConfig) (DirectoryReport, error) {
	if err := ctx.Err(); err != nil {
		return DirectoryReport{}, err
	}
	explicit := make(map[string]bool)
	for path := range dirCfg.fwCfg.OnlyFilepaths() {
		if filepath.Ext(path) != ".go" {
			return DirectoryReport{}, fmt.Errorf("not a Go source file: %s", path)
		}
		if _, err := safeFile(path); err != nil {
			return DirectoryReport{}, err
		}
		explicit[path] = true
	}
	var paths []string
	var mu sync.Mutex
	dirCfg.fwCfg.WorkFunc = func(ctx context.Context, path string) error {
		if err := ctx.Err(); err != nil {
			return err
		}
		if filepath.Ext(path) != ".go" {
			return nil
		}
		if _, err := safeFile(path); err != nil {
			return err
		}
		mu.Lock()
		paths = append(paths, path)
		mu.Unlock()
		return nil
	}
	if err := fastwalk.WalkContext(ctx, dirCfg.fwCfg); err != nil {
		return DirectoryReport{}, err
	}
	report, _, err := rewriteProject(ctx, srcCfg, paths, explicit)
	for _, path := range paths {
		delete(explicit, path)
	}
	for path := range explicit {
		report.Diagnostics = append(report.Diagnostics, Diagnostic{Position: token.Position{Filename: path}, Message: "explicit file excluded by traversal root or filters"})
	}
	return report, err
}
