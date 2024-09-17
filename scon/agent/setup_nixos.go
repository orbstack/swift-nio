package agent

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"regexp"
	"strings"

	"github.com/orbstack/macvirt/scon/agent/templates"
	"github.com/orbstack/macvirt/scon/util"
	"github.com/orbstack/macvirt/vmgr/conf/mounts"
	"github.com/sirupsen/logrus"
)

var identifier = regexp.MustCompile(`^[a-zA-Z_][a-zA-Z0-9_'-]*$`)

func configureSystemNixos(args InitialSetupArgs) error {
	logrus.Debug("Writing orbstack.nix")

	err := os.WriteFile("/etc/nixos/orbstack.nix", templates.OrbstackNix, 0644)
	if err != nil {
		return err
	}

	logrus.Debug("Writing configuration.nix")

	// can't use builtins.readFile in pure flakes, w/o inputs.*
	extraCertsData, err := os.ReadFile(mounts.ExtraCerts)
	if err != nil {
		return err
	}

	type configurationTemplateData struct {
		ConfigFile   string
		Username     string
		UsernameAttr string
		Password     string
		NoPassword   bool
		Timezone     string
		Certificates string
		StateVersion string
		UID          int
	}

	// TODO(winter): Remove when we drop 24.05?
	configFile := ""
	for _, f := range []string{"incus.nix", "lxd.nix"} {
		if _, err = os.Stat("/etc/nixos/" + f); err == nil {
			configFile = f
			break
		}
	}
	if configFile == "" {
		return errors.New("couldn't find container-specific configuration")
	}

	usernameAttr := args.Username
	if !identifier.MatchString(usernameAttr) {
		usernameAttr = `"` + args.Username + `"`
	}

	password := ""
	if args.Password != "" {
		hashedPassword, err := util.RunWithInput(args.Password, "mkpasswd", "-s")
		if err != nil {
			return err
		}

		password = strings.TrimSpace(hashedPassword)
	}

	var configuration bytes.Buffer
	err = templates.NixOSConfiguration.Execute(&configuration, configurationTemplateData{
		ConfigFile:   configFile,
		Username:     args.Username,
		UsernameAttr: usernameAttr,
		Password:     password,
		NoPassword:   password == "",
		Timezone:     args.Timezone,
		Certificates: string(extraCertsData),
		StateVersion: args.Version,
		UID:          args.Uid,
	})
	if err != nil {
		return fmt.Errorf("execute template: %w", err)
	}

	err = os.WriteFile("/etc/nixos/configuration.nix", configuration.Bytes(), 0644)
	if err != nil {
		return err
	}

	// rebuild
	logrus.Debug("Rebuilding system")
	err = util.Run("sh", "-lc", "nixos-rebuild boot")
	if err != nil {
		return err
	}

	return nil
}
