package fastwalk

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"sync"
)

type Config struct {
	RootFilepath   string
	WorkFunc       func(ctx context.Context, filepath string) error
	NumWorkers     int
	IncludeHidden  bool
	OnlyExtensions map[string]struct{}
}

func Walk(ctx context.Context, config Config) error {
	var wg sync.WaitGroup
	filepathChan := make(chan string, 100)
	errChan := make(chan error, 1)
	// wrap WorkFunc with wait group
	work := func() {
		defer wg.Done()
		// do work as new filepaths come into the channel
		for filepath := range filepathChan {
			if err := config.WorkFunc(ctx, filepath); err != nil {
				select {
				case errChan <- err:
				default:
				}
				continue
			}
		}
	}
	// spawn workers
	for i := 0; i < config.NumWorkers; i++ {
		wg.Add(1)
		go work()
	}
	// traverse and fill channel with filepaths
	go func() {
		defer close(filepathChan)
		err := filepath.WalkDir(config.RootFilepath,
			func(path string, d os.DirEntry, err error) error {
				if err != nil {
					select {
					case errChan <- err:
					default:
					}
					return nil
				}
				if !config.IncludeHidden && strings.HasPrefix(d.Name(), ".") {
					if d.IsDir() {
						return filepath.SkipDir
					}
					return nil
				}
				if len(config.OnlyExtensions) != 0 {
					if _, ok := config.OnlyExtensions[filepath.Ext(path)]; !ok {
						return nil
					}
				}
				filepathChan <- path
				return nil
			})
		if err != nil {
			select {
			case errChan <- err:
			default:
			}
		}
	}()
	wg.Wait()
	close(errChan)
	if err, ok := <-errChan; ok {
		return err
	}
	return nil
}
