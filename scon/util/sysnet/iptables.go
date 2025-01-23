package sysnet

import (
	"fmt"
	"regexp"
	"strconv"

	"github.com/orbstack/macvirt/scon/util"
	"github.com/sirupsen/logrus"
)

// matches:
// - "port 80" (works for dport and sport)
// - "80 :" (works for maps)
// - "80," (works for sets and maps)
// - "80 }" (works for end values of maps and sets)
// - ":80 "
var portRegex = regexp.MustCompile(`port\s(\d+)\b|\s(\d+)\s[:}]|\s(\d+),|:(\d+)\s`)

// this function also works for iptables
func ParseNftablesRules(ports map[uint16]struct{}, rulesStr string) error {
	matches := portRegex.FindAllStringSubmatch(rulesStr, -1)
	for _, match := range matches {
		// each possible match has a nonempty group
		// first match is full match, rest are match groups
		for _, group := range match[1:] {
			if group == "" {
				continue
			}

			portStr := group
			port, err := strconv.ParseUint(portStr, 10, 16)

			if err != nil {
				logrus.WithFields(logrus.Fields{
					"port": portStr,
				}).WithError(err).Debug("failed to parse port")
				continue
			}

			ports[uint16(port)] = struct{}{}
		}
	}

	return nil
}

func GetNftablesPorts(ports map[uint16]struct{}) error {
	rulesStr, err := util.RunWithOutput("nft", "list", "ruleset")
	if err != nil {
		return fmt.Errorf("get nftables rules: %w", err)
	}

	if err := ParseNftablesRules(ports, rulesStr); err != nil {
		return fmt.Errorf("parse nftables rules: %w", err)
	}

	return nil
}

func GetIptablesPorts(ports map[uint16]struct{}) error {
	rules4Str, err := util.RunWithOutput("iptables", "-L", "-v", "-n")
	if err != nil {
		return fmt.Errorf("get iptables rules: %w", err)
	}

	rules6Str, err := util.RunWithOutput("ip6tables", "-L", "-v", "-n")
	if err != nil {
		return fmt.Errorf("get ip6tables rules: %w", err)
	}

	if err := ParseNftablesRules(ports, rules4Str); err != nil {
		return fmt.Errorf("parse iptables rules: %w", err)
	}

	if err := ParseNftablesRules(ports, rules6Str); err != nil {
		return fmt.Errorf("parse ip6tables rules: %w", err)
	}

	return nil
}
