package sshpath

import (
	"regexp"
	"strings"
)

var (
	pathArgRegexp = regexp.MustCompile(`^([a-zA-Z0-9_\-]+)?=(/.+)$`)
)

func isPathArg(arg string) bool {
	// 1. starts with slash
	if strings.HasPrefix(arg, "/") {
		return true
	}

	// 2. -option=/value, --option=/value, or option=/value
	if pathArgRegexp.Match([]byte(arg)) {
		return true
	}

	return false
}

func TranslateArgs[T any](args []string, transFn PathTranslatorFunc[T], opts T) []string {
	for i, arg := range args {
		if isPathArg(arg) {
			if pathArgRegexp.Match([]byte(arg)) {
				// -option=/value, --option=/value, or option=/value
				matches := pathArgRegexp.FindStringSubmatch(arg)
				args[i] = matches[1] + "=" + transFn(matches[2], opts)
			} else {
				args[i] = transFn(arg, opts)
			}
		}
	}

	return args
}
