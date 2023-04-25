package envutil

import (
	"sort"
	"strings"
)

type EnvMap map[string]string

func NewMap() EnvMap {
	return make(map[string]string)
}

func ToMap(envPairs []string) EnvMap {
	m := make(map[string]string, len(envPairs))
	for _, e := range envPairs {
		k, v, ok := strings.Cut(e, "=")
		if !ok {
			continue
		}
		m[k] = v
	}
	return m
}

func (m EnvMap) ToPairs() []string {
	out := make([]string, 0, len(m))
	for k, v := range m {
		out = append(out, k+"="+v)
	}
	// sort
	sort.Strings(out)

	return out
}

func (m EnvMap) SetPair(kv string) {
	k, v, ok := strings.Cut(kv, "=")
	if !ok {
		return
	}
	m[k] = v
}
