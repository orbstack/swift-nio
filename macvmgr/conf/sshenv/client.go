package sshenv

import (
	"strings"

	"github.com/kdrag0n/macvirt/scon/agent/envutil"
	"github.com/sirupsen/logrus"
)

type TranslatorFuncs struct {
	Proxy ProxyTranslatorFunc
}

var (
	ToMac = TranslatorFuncs{
		Proxy: ProxyToMac,
	}
	ToLinux = TranslatorFuncs{
		Proxy: ProxyToLinux,
	}
)

func OSToClientEnv(osEnv envutil.EnvMap, transFns TranslatorFuncs) (envutil.EnvMap, error) {
	// new empty client map
	clientEnv := envutil.NewMap()

	// add anything that we pass by default
	for _, k := range defaultPassEnvKeys {
		if v, ok := osEnv[k]; ok {
			clientEnv[k] = v
		}
	}

	// translate proxy envs
	for _, k := range proxyEnvKeys {
		if v, ok := osEnv[k]; ok {
			newValue, err := translateOneProxyUrl(v, transFns.Proxy)
			if err != nil {
				logrus.WithError(err).WithField("key", k).Warn("failed to translate proxy url")
				continue
			}

			clientEnv[k] = newValue
		}
	}

	// extract ORBENV (preferred) or WSLENV (compat)
	userCtl := osEnv["ORBENV"]
	if userCtl == "" {
		userCtl = osEnv["WSLENV"]
	}
	if userCtl != "" {
		// parse list
		extraSpecs := strings.Split(userCtl, ":")
		for _, spec := range extraSpecs {
			// flags
			// ignore ok - error case returns (spec, "", false) which is what we want
			key, flags, _ := strings.Cut(spec, "/")

			// TODO implement flags
			_ = flags
			if v, ok := osEnv[key]; ok {
				clientEnv[key] = v
			}
		}
	}

	return clientEnv, nil
}
