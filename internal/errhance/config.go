package errhance

import "github.com/alexander-bachmann/errhance/internal/fastwalk"

type DirectoryConfig struct {
	fwCfg fastwalk.Config
}

type DirectoryOpt func(*DirectoryConfig)

func NewDirectoryConfig(fwCfg fastwalk.Config, opts ...DirectoryOpt) DirectoryConfig {
	cfg := DirectoryConfig{fwCfg: fwCfg}
	for _, opt := range opts {
		opt(&cfg)
	}
	return cfg
}

type SourceConfig struct {
	overwrite           bool
	forceOverwrite      bool
	generated           bool
	includeRelativePath bool
}

type SourceOpt func(*SourceConfig)

func NewSourceConfig(opts ...SourceOpt) SourceConfig {
	cfg := SourceConfig{
		overwrite:           false,
		forceOverwrite:      false,
		generated:           false,
		includeRelativePath: false,
	}
	for _, opt := range opts {
		opt(&cfg)
	}
	return cfg
}

func WithOverwrite(b bool) SourceOpt      { return func(cfg *SourceConfig) { cfg.overwrite = b } }
func WithForceOverwrite(b bool) SourceOpt { return func(cfg *SourceConfig) { cfg.forceOverwrite = b } }
func WithGenerated(b bool) SourceOpt      { return func(cfg *SourceConfig) { cfg.generated = b } }
func WithIncludeRelativePath(b bool) SourceOpt {
	return func(cfg *SourceConfig) { cfg.includeRelativePath = b }
}
