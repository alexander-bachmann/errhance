package fastwalk

import (
	"context"
	"os"
	"path/filepath"
	"strings"
)

type Config struct {
	WorkFunc           func(ctx context.Context, path string) error
	rootFilepath       string
	numWorkers         int
	includeHidden      bool
	includeDirectories bool
	onlyExtensions     map[string]struct{}
	onlyFilepaths      map[string]struct{}
	skipDirectories    map[string]struct{}
}

type Option func(*Config)

func NewConfig(workFunc func(ctx context.Context, path string) error, opts ...Option) Config {
	wd, err := os.Getwd()
	if err != nil {
		wd = "." // fallback to current directory if os.Getwd() fails
	}
	cfg := Config{
		WorkFunc:           workFunc,
		rootFilepath:       wd,
		numWorkers:         4,
		includeHidden:      false,
		includeDirectories: false,
		onlyExtensions:     make(map[string]struct{}),
		onlyFilepaths:      make(map[string]struct{}),
		skipDirectories:    make(map[string]struct{}),
	}
	for _, opt := range opts {
		opt(&cfg)
	}
	return cfg
}

func WithRootFilepath(path string) Option {
	return func(cfg *Config) { cfg.rootFilepath = path }
}

func WithNumWorkers(n int) Option {
	return func(cfg *Config) { cfg.numWorkers = n }
}

func WithIncludeHidden(b bool) Option {
	return func(cfg *Config) { cfg.includeHidden = b }
}

func WithIncludeDirectories(b bool) Option {
	return func(cfg *Config) { cfg.includeDirectories = b }
}

func WithOnlyExtensions(exts []string) Option {
	return func(cfg *Config) {
		for _, ext := range exts {
			if !strings.HasPrefix(ext, ".") {
				ext = "." + ext
			}
			cfg.onlyExtensions[ext] = struct{}{}
		}
	}
}

func WithOnlyFilepaths(paths []string) Option {
	return func(cfg *Config) {
		wd, _ := os.Getwd()
		for _, path := range paths {
			if !filepath.IsAbs(path) {
				path = filepath.Join(wd, path)
			}
			cfg.onlyFilepaths[path] = struct{}{}
		}
	}
}

func WithSkipDirectories(dirs []string) Option {
	return func(cfg *Config) {
		for _, dir := range dirs {
			cfg.skipDirectories[dir] = struct{}{}
		}
	}
}
