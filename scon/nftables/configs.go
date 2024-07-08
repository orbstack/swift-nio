package nftables

import _ "embed"

//go:embed nft_vm.conf
var ConfigVM string

//go:embed nft_docker.conf
var ConfigDocker string
