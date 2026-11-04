package control

import "time"

// Config is the plain data a testbed's command fills from flags, environment variables or a file. The zero value of
// a field means its default, which reproduces the launchpad of flagd-testbed v3.11.0.
type Config struct {
	// Address is where the control API listens. Default ":8080".
	Address string `json:"address"`
	// Configs is the directory of the configurations a /start names, as <name>.json. Default "/configs". A name
	// without a file there is an unknown configuration, answered at once.
	Configs string `json:"configs"`
	// NoConfigs turns that check off: the target then decides which configurations exist, and says so by returning
	// ErrUnknownConfiguration from Start.
	NoConfigs bool `json:"noConfigs"`
	// RawFlags is the directory of the flag files that are merged into the combined flag document. Default
	// "/rawflags". Files whose name starts with "selector-" are not part of it.
	RawFlags string `json:"rawFlags"`
	// Render is the file the combined flag document is rendered to. Default "/flags/allFlags.json". Set NoRender for
	// a target that has no provider reading a file.
	Render string `json:"render"`
	// NoRender turns the rendering off.
	NoRender bool `json:"noRender"`
	// DefaultConfig is the configuration the server starts with and a bare /start uses. Default "default".
	DefaultConfig string `json:"defaultConfig"`
	// ChangingFlag is the flag /change toggles. Default "changing-flag".
	ChangingFlag string `json:"changingFlag"`
	// ChangingVariants are the two default variants /change toggles between. Default "foo" and "bar".
	ChangingVariants [2]string `json:"changingVariants"`
	// StartTimeout is how long a start may take. Default 10s.
	StartTimeout time.Duration `json:"startTimeout"`
	// ChangeTimeout is how long /change waits for the target to take in the new document. Default 5s.
	ChangeTimeout time.Duration `json:"changeTimeout"`
	// DefaultRestartSeconds is the delay of /restart without a seconds parameter. Default 5; a value below one
	// means the default.
	DefaultRestartSeconds int `json:"defaultRestartSeconds"`
}

// WithDefaults returns the configuration with the default for every field left at its zero value.
func (c Config) WithDefaults() Config {
	if c.Address == "" {
		c.Address = ":8080"
	}
	if c.NoConfigs {
		c.Configs = ""
	} else if c.Configs == "" {
		c.Configs = "/configs"
	}
	if c.RawFlags == "" {
		c.RawFlags = "/rawflags"
	}
	if c.Render == "" {
		c.Render = "/flags/allFlags.json"
	}
	if c.NoRender {
		c.Render = ""
	}
	if c.DefaultConfig == "" {
		c.DefaultConfig = "default"
	}
	if c.ChangingFlag == "" {
		c.ChangingFlag = "changing-flag"
	}
	if c.ChangingVariants == [2]string{} {
		c.ChangingVariants = [2]string{"foo", "bar"}
	}
	if c.StartTimeout <= 0 {
		c.StartTimeout = 10 * time.Second
	}
	if c.ChangeTimeout <= 0 {
		c.ChangeTimeout = 5 * time.Second
	}
	if c.DefaultRestartSeconds < 1 {
		c.DefaultRestartSeconds = 5
	}
	return c
}
