package errhance

type Config struct {
	// Enhances both `return err`s and safe `return fmt.Errorf`s.
	// Safe fmt.Errorf example: `return fmt.Errorf("failed: %w", err)`
	overwrite bool
	// Enhances `return err`s, safe `return fmt.Errorf`s, and unsafe `return fmt.Errorf`s.
	// Unsafe fmt.Errorf example: `return fmt.Errorf("failed: %w", ErrNotFound)`
	// Note: This example is considered unsafe because it is probable that
	// one of the functions on the call stack depend on the wrapped error
	// via errors.Is() or errors.As().
	forceOverwrite bool
	// Enhances generated code
	generated bool
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
