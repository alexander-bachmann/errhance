package fastwalk

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"sync"
)

func Walk(cfg Config) error { return WalkContext(context.Background(), cfg) }

func WalkContext(ctx context.Context, cfg Config) error {
	// create a cancelable context to allow early termination if an error occurs
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	var wg sync.WaitGroup
	// buffered channel to send file paths from the directory walker to workers
	pathChan := make(chan string, 100)
	// channel to report errors (buffered to ensure the first error is captured)
	errChan := make(chan error, 1)

	// worker() processes file paths sent on pathChan.
	worker := func() {
		defer wg.Done()
		for {
			select {
			// check if the context is canceled to exit early
			case <-ctx.Done():
				return
			// receive a file path from the channel
			case p, ok := <-pathChan:
				if !ok {
					// channel closed, no more paths to process
					return
				}
				// execute the provided work function.
				if err := cfg.workFunc(ctx, p); err != nil {
					// on error, send the error to errChan and cancel context to stop further work
					select {
					case errChan <- err:
						cancel()
					default:
					}
				}
			}
		}
	}

	// start the workers
	for i := 0; i < cfg.numWorkers; i++ {
		wg.Add(1)
		go worker()
	}

	// start a goroutine to traverse the directory tree
	go func() {
		// once done, close the channel to signal no more file paths
		defer close(pathChan)
		err := filepath.WalkDir(cfg.rootFilepath, func(path string, d os.DirEntry, err error) error {
			if err != nil {
				// if there is an error accessing a file or directory, report it
				select {
				case errChan <- err:
					cancel()
				default:
				}
				return nil
			}
			// skip hidden files or hidden directories if includeHidden is false.
			if !cfg.includeHidden && strings.HasPrefix(d.Name(), ".") {
				if d.IsDir() {
					// skip entire directory
					return filepath.SkipDir
				}
				// skip hidden file
				return nil
			}
			// skip directories if includeDirectories is false
			if !cfg.includeDirectories && d.IsDir() {
				return nil
			}
			// if onlyExtensions is set, filter out files without matching extension
			if len(cfg.onlyExtensions) > 0 {
				if _, ok := cfg.onlyExtensions[filepath.Ext(path)]; !ok {
					return nil
				}
			}
			// send the file path to the worker pool
			select {
			case pathChan <- path:
			case <-ctx.Done():
				// exit early if context has been canceled
				return ctx.Err()
			}
			return nil
		})
		// report any error that occurred during the directory walk
		if err != nil {
			select {
			case errChan <- err:
				cancel()
			default:
			}
		}
	}()

	// wait for all workers to finish
	wg.Wait()
	// close error channel as no more errors will be sent
	close(errChan)
	// return the first error encountered, if any
	if err, ok := <-errChan; ok {
		return err
	}
	return nil
}
