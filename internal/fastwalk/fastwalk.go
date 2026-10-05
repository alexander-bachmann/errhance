package fastwalk

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
)

func Walk(cfg Config) error { return WalkContext(context.Background(), cfg) }

// WalkContext visits regular files and, when requested, directories using cleaned
// absolute paths. The explicit root is exempt from hidden-name filtering.
// Symlink and special-file roots are rejected; such descendants are skipped.
// These checks do not protect against concurrent filesystem replacement.
// Cancellation stops scheduling work and waits for active callbacks to return.
func WalkContext(ctx context.Context, cfg Config) error {
	if cfg.numWorkers <= 0 {
		return fmt.Errorf("fastwalk: number of workers must be positive")
	}
	if cfg.WorkFunc == nil {
		return fmt.Errorf("fastwalk: work function must not be nil")
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	root, err := filepath.Abs(cfg.rootFilepath)
	if err != nil {
		return fmt.Errorf("fastwalk: root path: %w", err)
	}
	// WalkDir does not follow descendant symlinks, but the OS would follow
	// symlinks in the root's parent components without these checks.
	for path := root; ; path = filepath.Dir(path) {
		info, err := os.Lstat(path)
		if err != nil {
			return err
		}
		if info.Mode()&os.ModeSymlink != 0 || (!info.IsDir() && !info.Mode().IsRegular()) {
			return fmt.Errorf("fastwalk: unsupported root path %q (%s)", path, info.Mode())
		}
		if filepath.Dir(path) == path {
			break
		}
	}
	paths := make(map[string]struct{}, len(cfg.onlyFilepaths))
	for path := range cfg.onlyFilepaths {
		path, err := filepath.Abs(path)
		if err != nil {
			return fmt.Errorf("fastwalk: filter path: %w", err)
		}
		paths[path] = struct{}{}
	}

	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	var wg sync.WaitGroup
	pathChan := make(chan string, 100)
	var firstErr error
	var once sync.Once
	report := func(err error) {
		if err != nil {
			once.Do(func() {
				firstErr = err
				cancel()
			})
		}
	}
	for i := 0; i < cfg.numWorkers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				select {
				case <-ctx.Done():
					return
				case path, ok := <-pathChan:
					if !ok || ctx.Err() != nil {
						return
					}
					if err := cfg.WorkFunc(ctx, path); err != nil {
						report(err)
						return
					}
				}
			}
		}()
	}

	// The caller owns production and closes the queue before joining workers.
	err = filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if err != nil {
			return err
		}
		if d.IsDir() {
			if _, skip := cfg.skipDirectories[d.Name()]; skip {
				return filepath.SkipDir
			}
		}
		if path != root && !cfg.includeHidden && strings.HasPrefix(d.Name(), ".") {
			if d.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		if d.IsDir() {
			if !cfg.includeDirectories {
				return nil
			}
		} else {
			// A zero DirEntry type can mean unknown type; confirm via Info.
			if !d.Type().IsRegular() {
				return nil
			}
			info, err := d.Info()
			if err != nil {
				return err
			}
			if !info.Mode().IsRegular() {
				return nil
			}
		}
		if len(cfg.onlyExtensions) > 0 {
			if _, ok := cfg.onlyExtensions[filepath.Ext(path)]; !ok {
				return nil
			}
		}
		if len(paths) > 0 {
			if _, ok := paths[path]; !ok {
				return nil
			}
		}
		select {
		case pathChan <- path:
			return nil
		case <-ctx.Done():
			return ctx.Err()
		}
	})
	report(err)
	close(pathChan)
	wg.Wait()
	if firstErr != nil {
		return firstErr
	}
	return ctx.Err()
}
