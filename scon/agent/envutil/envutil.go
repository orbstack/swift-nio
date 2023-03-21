package envutil

import (
	"sort"
	"strings"
)

func Dedupe(env []string) []string {
	// later ones override earlier ones
	// easiest to use a map
	m := make(map[string]string)
	for _, e := range env {
		k, v, ok := strings.Cut(e, "=")
		if !ok {
			continue
		}
		m[k] = v
	}
	// convert back to slice
	out := make([]string, 0, len(m))
	for k, v := range m {
		out = append(out, k+"="+v)
	}
	// sort
	sort.Strings(out)

	return out
}
