package errhance

type Config struct {
	overwrite      bool
	forceOverwrite bool
	generated      bool
}

type Option func(*Config)

func NewConfig(opts ...Option) Config {
	cfg := Config{
		overwrite:      false,
		forceOverwrite: false,
		generated:      false,
	}
	for _, opt := range opts {
		opt(&cfg)
	}
	return cfg
}

func WithOverwrite(b bool) Option      { return func(cfg *Config) { cfg.overwrite = b } }
func WithForceOverwrite(b bool) Option { return func(cfg *Config) { cfg.forceOverwrite = b } }
func WithGenerated(b bool) Option      { return func(cfg *Config) { cfg.generated = b } }
