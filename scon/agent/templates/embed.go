package templates

import (
	_ "embed"
	"text/template"
)

//go:embed nixos-configuration.nix
var nixOSConfigurationTemplate string

//go:embed nixos-orbstack.nix
var OrbstackNix []byte

var NixOSConfiguration = template.Must(template.New("configuration.nix").Parse(nixOSConfigurationTemplate))
