package main

import (
	"github.com/hashicorp/hcl2/gohcl"
	"github.com/hashicorp/hcl2/hclparse"
)

// some hcl2 documentation can be found here:
// https://godoc.org/github.com/hashicorp/hcl2/gohcl

// Config is parsed from a configuration file.
type Config struct {
	TLSCertificateFile *string `hcl:"tls_certificate_file"`
	TLSKeyFile         *string `hcl:"tls_key_file"`
	TLSEnable          *bool   `hcl:"tls_enable"`

	Paths []Path `hcl:"path,block"`
}

// Path configures one sub-path of the proxy.
type Path struct {
	Path string `hcl:",label"`
	URL  string `hcl:"url"`
}

// DefaultConfig collects default config items.
var DefaultConfig = Config{}

// ParseConfig returns a config from a file.
func ParseConfig(filename string) (Config, error) {
	var cfg = DefaultConfig

	parser := hclparse.NewParser()
	file, diags := parser.ParseHCLFile(filename)

	if len(diags) != 0 {
		return Config{}, diags
	}

	decodeDiags := gohcl.DecodeBody(file.Body, nil, &cfg)
	diags = append(diags, decodeDiags...)
	if diags.HasErrors() {
		return Config{}, diags
	}

	return cfg, nil
}
