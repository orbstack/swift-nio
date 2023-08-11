package templates

import (
	_ "embed"
	"html/template"
)

//go:embed dns_index.html
var dnsIndexHTML string

var DnsIndexHTML = template.Must(template.New("dns_index.html").Parse(dnsIndexHTML))
