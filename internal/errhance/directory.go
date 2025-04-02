package errhance

import (
	"context"
	"fmt"
	"sync/atomic"

	"github.com/alexander-bachmann/errhance/internal/fastwalk"
)

type DirectoryReport struct {
	TotalFilesModified  int
	TotalErrorsEnhanced int
}

func LocalDirectory(dirCfg DirectoryConfig, srcCfg SourceConfig) (DirectoryReport, error) {
	var totalFilesModified atomic.Int64
	var totalErrorsEnhanced atomic.Int64
	// closure for counting
	dirCfg.fwCfg.WorkFunc = func(_ context.Context, path string) error {
		srcReport, err := LocalFile(srcCfg, path)
		if err != nil {
			return fmt.Errorf("LocalFile: %s: %w", path, err)
		}
		if srcReport.TotalErrorsEnhanced > 0 {
			totalFilesModified.Add(1)
		}
		totalErrorsEnhanced.Add(int64(srcReport.TotalErrorsEnhanced))
		return nil
	}
	// reconsider if this matters
	// valid, err := inGoProject()
	// if err != nil || !valid {
	// 	return DirectoryReport{}, err
	// }

	// downloadGoImportsIfMissing should be in LocalFile
	// because that is the function with a dependency on
	// goimports but it does not make sense to check for
	// goimports more than once
	err := downloadGoimportsIfMissing()
	if err != nil {
		return DirectoryReport{}, fmt.Errorf("downloadGoimportsIfMissing: %w", err)
	}
	err = fastwalk.Walk(dirCfg.fwCfg)
	if err != nil {
		return DirectoryReport{}, fmt.Errorf("fastwalk.Walk: %w", err)
	}
	return DirectoryReport{
		TotalFilesModified:  int(totalFilesModified.Load()),
		TotalErrorsEnhanced: int(totalErrorsEnhanced.Load()),
	}, nil
}
