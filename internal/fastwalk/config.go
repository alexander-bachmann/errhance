package fastwalk

import (
	"context"
	"os"
	"strings"
)

type Config struct {
	workFunc           func(ctx context.Context, path string) error
	rootFilepath       string
	numWorkers         int
	includeHidden      bool
	includeDirectories bool
	onlyExtensions     map[string]struct{}
	onlyFilepaths      map[string]struct{}
}

type Option func(*Config)

func NewConfig(workFunc func(ctx context.Context, path string) error, opts ...Option) Config {
	wd, err := os.Getwd()
	if err != nil {
		wd = "." // fallback to current directory if os.Getwd() fails
	}
	cfg := Config{
		workFunc:           workFunc,
		rootFilepath:       wd,
		numWorkers:         4,
		includeHidden:      false,
		includeDirectories: false,
		onlyExtensions:     make(map[string]struct{}),
		onlyFilepaths:      make(map[string]struct{}),
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
		for _, path := range paths {
			cfg.onlyFilepaths[path] = struct{}{}
		}
	}
}
