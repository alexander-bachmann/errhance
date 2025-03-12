package errhance

type Config struct {
	// errhance `return err` OR `return fmt.Errorf()`
	overwrite bool
	// revert `return fmt.Errorf()` to `return err`
	revert bool
	// errhance generated code
	generatedCode bool
}

type Option func(*Config)

func NewConfig(opts ...Option) Config {
	cfg := Config{
		overwrite:     false,
		revert:        false,
		generatedCode: false,
	}
	for _, opt := range opts {
		opt(&cfg)
	}
	return cfg
}

func WithOverwrite() Option     { return func(cfg *Config) { cfg.overwrite = true } }
func WithRevert() Option        { return func(cfg *Config) { cfg.revert = true } }
func WithGeneratedCode() Option { return func(cfg *Config) { cfg.generatedCode = true } }
