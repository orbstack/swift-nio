package main

import (
	"crypto/ed25519"
	"encoding/pem"
	"errors"
	"fmt"
	"os"
	"strings"

	"github.com/kdrag0n/macvirt/macvmgr/conf"
	"github.com/kdrag0n/macvirt/macvmgr/conf/appid"
	"github.com/kdrag0n/macvirt/macvmgr/conf/ports"
	"github.com/mikesmitty/edkey"
	"golang.org/x/crypto/ssh"
)

var (
	sshConfigSegment = fmt.Sprintf(`Host %s
  Hostname 127.0.0.1
  Port %d
  # SSH user syntax:
  #   <container>@%s to connect to <container> as the default user (matching your macOS user)
  #   <user>@<container>@%s to connect to <container> as <user>
  # Examples:
  #   ubuntu@%s: container "ubuntu", user matching your macOS user
  #   root@fedora@%s: container "fedora", user "root"
  User default
  IdentityFile %s/id_ed25519
`, appid.AppName, ports.HostSconSSHPublic, appid.AppName, appid.AppName, appid.AppName, appid.AppName, makeHomeRelative(conf.ExtraSshDir()))

	sshConfigIncludeLine = fmt.Sprintf("Include %s/config", makeHomeRelative(conf.ExtraSshDir()))
)

func makeHomeRelative(path string) string {
	return strings.Replace(path, conf.HomeDir(), "~", 1)
}

func generatePublicSSHKey() error {
	pk, sk, err := ed25519.GenerateKey(nil)
	if err != nil {
		return err
	}

	sshPk, err := ssh.NewPublicKey(pk)
	if err != nil {
		return err
	}

	pemKey := &pem.Block{
		Type:  "OPENSSH PRIVATE KEY",
		Bytes: edkey.MarshalED25519PrivateKey(sk),
	}
	sshSkText := pem.EncodeToMemory(pemKey)
	sshPkText := ssh.MarshalAuthorizedKey(sshPk)

	err = os.WriteFile(conf.ExtraSshDir()+"/id_ed25519", sshSkText, 0600)
	if err != nil {
		return err
	}
	err = os.WriteFile(conf.ExtraSshDir()+"/id_ed25519.pub", sshPkText, 0644)
	if err != nil {
		return err
	}

	return nil
}

func setupPublicSSH() error {
	// write extra config
	err := os.WriteFile(conf.ExtraSshDir()+"/config", []byte(sshConfigSegment), 0644)
	if err != nil {
		return err
	}

	// add include if necessary
	userConfigPath := conf.UserSshDir() + "/config"
	sshConfig, err := os.ReadFile(userConfigPath)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			sshConfig = []byte{}
		} else {
			return err
		}
	}
	if !strings.Contains(string(sshConfig), sshConfigIncludeLine) {
		// prepend, or it doesn't work
		sshConfig = append([]byte(sshConfigIncludeLine+"\n\n"), sshConfig...)
		err = os.WriteFile(userConfigPath, sshConfig, 0644)
		if err != nil {
			return err
		}
	}

	// generate key if necessary
	_, err1 := os.Stat(conf.ExtraSshDir() + "/id_ed25519")
	_, err2 := os.Stat(conf.ExtraSshDir() + "/id_ed25519.pub")
	if errors.Is(err1, os.ErrNotExist) || errors.Is(err2, os.ErrNotExist) {
		err = generatePublicSSHKey()
		if err != nil {
			return err
		}
	}

	return nil
}
