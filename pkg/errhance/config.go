package errhance

type Config struct {
	// source code
	src string
	// force regenerate `return fmt.Errorf()` regardless of if it the already exists
	force bool
	// revert `return fmt.Errorf()` to `return err`
	revert bool
	// errhance generated code
	generatedCode bool
}

type Option func(*Config)

func NewConfig(src string, opts ...Option) Config {
	cfg := Config{
		src:           src,
		force:         false,
		revert:        false,
		generatedCode: false,
	}
	for _, opt := range opts {
		opt(&cfg)
	}
	return cfg
}

func WithForce() Option         { return func(cfg *Config) { cfg.force = true } }
func WithRevert() Option        { return func(cfg *Config) { cfg.revert = true } }
func WithGeneratedCode() Option { return func(cfg *Config) { cfg.generatedCode = true } }
