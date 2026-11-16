package config

import _ "embed"

// templateText is the configuration template written by `tg config init`. It is
// the only copy: the command, the tests and the documentation read it from here.
//
//go:embed template.toml
var templateText string

// Template returns the configuration template. It is valid TOML: every setting
// is commented out, so loading it gives the defaults.
func Template() string { return templateText }
