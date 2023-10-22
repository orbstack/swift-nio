package hclient

import (
	"encoding/json"
	"net"
	"net/rpc"

	"github.com/orbstack/macvirt/scon/sgclient/sgtypes"
	"github.com/orbstack/macvirt/vmgr/drm/drmtypes"
	"github.com/orbstack/macvirt/vmgr/guihelper/guitypes"
	"github.com/orbstack/macvirt/vmgr/uitypes"
	"github.com/orbstack/macvirt/vmgr/vnet/services/hcontrol/htypes"
)

type Client struct {
	rpc             *rpc.Client
	user            *htypes.User
	sshAgentSockets *htypes.SSHAgentSockets
}

func (c *Client) Ping() error {
	var none None
	return c.rpc.Call("hc.Ping", none, &none)
}

func (c *Client) StartForward(spec ForwardSpec) error {
	var none None
	return c.rpc.Call("hc.StartForward", spec, &none)
}

func (c *Client) StopForward(spec ForwardSpec) error {
	var none None
	return c.rpc.Call("hc.StopForward", spec, &none)
}

func (c *Client) GetUser() (*htypes.User, error) {
	if c.user != nil {
		return c.user, nil
	}

	var u htypes.User
	err := c.rpc.Call("hc.GetUser", None{}, &u)
	if err != nil {
		return nil, err
	}

	c.user = &u
	return &u, nil
}

func (c *Client) GetTimezone() (string, error) {
	var tz string
	err := c.rpc.Call("hc.GetTimezone", None{}, &tz)
	if err != nil {
		return "", err
	}

	return tz, nil
}

func (c *Client) GetSSHAuthorizedKeys() (string, error) {
	var key string
	err := c.rpc.Call("hc.GetSSHAuthorizedKeys", None{}, &key)
	if err != nil {
		return "", err
	}

	return key, nil
}

func (c *Client) GetSSHAgentSockets() (*htypes.SSHAgentSockets, error) {
	if c.sshAgentSockets != nil {
		return c.sshAgentSockets, nil
	}

	var socks htypes.SSHAgentSockets
	err := c.rpc.Call("hc.GetSSHAgentSockets", None{}, &socks)
	if err != nil {
		return &htypes.SSHAgentSockets{}, err
	}

	c.sshAgentSockets = &socks
	return &socks, nil
}

func (c *Client) GetGitConfig() (map[string]string, error) {
	var config map[string]string
	err := c.rpc.Call("hc.GetGitConfig", None{}, &config)
	if err != nil {
		return nil, err
	}

	return config, nil
}

func (c *Client) GetLastDrmResult() (*drmtypes.Result, error) {
	var result drmtypes.Result
	err := c.rpc.Call("hc.GetLastDrmResult", None{}, &result)
	if err != nil {
		return nil, err
	}

	return &result, nil
}

func (c *Client) GetDockerMachineConfig() (htypes.DockerMachineConfig, error) {
	var result htypes.DockerMachineConfig
	err := c.rpc.Call("hc.GetDockerMachineConfig", None{}, &result)
	if err != nil {
		return htypes.DockerMachineConfig{}, err
	}

	return result, nil
}

func (c *Client) GetExtraCaCertificates() ([]string, error) {
	var result []string
	err := c.rpc.Call("hc.GetExtraCaCertificates", None{}, &result)
	if err != nil {
		return nil, err
	}

	return result, nil
}

func (c *Client) Notify(n guitypes.Notification) error {
	var none None
	return c.rpc.Call("hc.Notify", n, &none)
}

func (c *Client) AddFsnotifyRef(path string) error {
	var none None
	return c.rpc.Call("hc.AddFsnotifyRef", path, &none)
}

func (c *Client) RemoveFsnotifyRef(path string) error {
	var none None
	return c.rpc.Call("hc.RemoveFsnotifyRef", path, &none)
}

func (c *Client) AddDockerBridge(config sgtypes.DockerBridgeConfig) (int, error) {
	var vlanId int
	err := c.rpc.Call("hc.AddDockerBridge", config, &vlanId)
	if err != nil {
		return 0, err
	}

	return vlanId, nil
}

func (c *Client) RemoveDockerBridge(config sgtypes.DockerBridgeConfig) (int, error) {
	var vlanId int
	err := c.rpc.Call("hc.RemoveDockerBridge", config, &vlanId)
	if err != nil {
		return 0, err
	}

	return vlanId, nil
}

func (c *Client) ClearDockerState(info htypes.DockerExitInfo) error {
	var none None
	return c.rpc.Call("hc.ClearDockerState", info, &none)
}

func (c *Client) OnUIEvent(ev uitypes.UIEvent) error {
	// XXX: encode to string here for perf across gob rpc
	data, err := json.Marshal(ev)
	if err != nil {
		return err
	}
	eventJsonStr := string(data)

	var none None
	return c.rpc.Call("hc.OnUIEvent", eventJsonStr, &none)
}

func (c *Client) OnNfsReady() error {
	var none None
	return c.rpc.Call("hc.OnNfsReady", None{}, &none)
}

func (c *Client) OnK8sConfigReady(kubeConfigStr string) error {
	var none None
	return c.rpc.Call("hc.OnK8sConfigReady", kubeConfigStr, &none)
}

func (c *Client) GetInitConfig() (*htypes.InitConfig, error) {
	var config htypes.InitConfig
	err := c.rpc.Call("hc.GetInitConfig", None{}, &config)
	if err != nil {
		return nil, err
	}

	return &config, nil
}

func (c *Client) GetTLSRootData() (*htypes.KeychainTLSData, error) {
	var data htypes.KeychainTLSData
	err := c.rpc.Call("hc.GetTLSRootData", None{}, &data)
	if err != nil {
		return nil, err
	}

	return &data, nil
}

func (c *Client) ImportCertificate(certPem string) error {
	var none None
	return c.rpc.Call("hc.ImportCertificate", certPem, &none)
}

func (c *Client) Close() error {
	return c.rpc.Close()
}

func New(conn net.Conn) (*Client, error) {
	rpcClient := rpc.NewClient(conn)
	return &Client{
		rpc: rpcClient,
	}, nil
}
