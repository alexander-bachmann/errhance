package errhance

type Config struct {
	// errhances both `return err` and `return fmt.Errorf()`
	// the default behavior of errhance is to change `return err`
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

func WithOverwrite(b bool) Option     { return func(cfg *Config) { cfg.overwrite = b } }
func WithRevert(b bool) Option        { return func(cfg *Config) { cfg.revert = b } }
func WithGeneratedCode(b bool) Option { return func(cfg *Config) { cfg.generatedCode = b } }
