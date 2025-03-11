package fastwalk

import (
	"context"
	"os"
)

type Config struct {
	workFunc       func(ctx context.Context, path string) error
	rootFilepath   string
	numWorkers     int
	includeHidden  bool
	onlyExtensions map[string]struct{}
}

type Option func(*Config)

func NewConfig(workFunc func(ctx context.Context, path string) error, opts ...Option) Config {
	wd, err := os.Getwd()
	if err != nil {
		wd = "." // fallback to current directory if os.Getwd() fails
	}
	cfg := Config{
		workFunc:       workFunc,
		rootFilepath:   wd,
		numWorkers:     4,
		includeHidden:  false,
		onlyExtensions: make(map[string]struct{}),
	}
	for _, opt := range opts {
		opt(&cfg)
	}
	return cfg
}

func WithRootFilepath(path string) Option {
	return func(cfg *Config) {
		cfg.rootFilepath = path
	}
}

func WithNumWorkers(n int) Option {
	return func(cfg *Config) {
		cfg.numWorkers = n
	}
}

func WithHidden() Option {
	return func(cfg *Config) {
		cfg.includeHidden = true
	}
}

func WithExtensions(exts []string) Option {
	return func(cfg *Config) {
		for _, ext := range exts {
			cfg.onlyExtensions[ext] = struct{}{}
		}
	}
}
