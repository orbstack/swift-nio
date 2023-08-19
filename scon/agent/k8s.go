package agent

import (
	"os"
	"time"

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
	err := util.WaitForPathExist("/run/kubeconfig.yml", true /*requireWriteClose*/)
	if err != nil {
		return err
	}

	// give it some time just to make sure it's fully written
	// k8s is slow to start anyway so it doesn't matter
	time.Sleep(100 * time.Millisecond)

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
