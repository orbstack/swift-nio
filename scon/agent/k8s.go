package agent

import (
	"os"

	"github.com/orbstack/macvirt/scon/util"
	"github.com/sirupsen/logrus"
)

type K8sAgent struct {
	docker *DockerAgent
}

func (a *K8sAgent) WaitAndSendKubeConfig() error {
	// wait for kubeconfig
	// parent /run always exists because we mount it in docker machine
	logrus.Debug("k8s: waiting for kubeconfig")
	err := util.WaitForPathExist("/run/kubeconfig.yml")
	if err != nil {
		return err
	}

	kubeConfigData, err := os.ReadFile("/run/kubeconfig.yml")
	if err != nil {
		return err
	}

	// send to host
	logrus.Debug("k8s: sending kubeconfig to host")
	err = a.docker.host.OnK8sConfigReady(string(kubeConfigData))
	if err != nil {
		return err
	}

	return nil
}
