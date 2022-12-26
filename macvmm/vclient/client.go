package vclient

import (
	"fmt"
	"net/http"

	"github.com/kdrag0n/macvirt/macvmm/vclient/iokit"
)

const (
	VcontrolPort = 103
	baseUrl      = "http://172.30.30.2:103/"
)

type VClient struct {
	client *http.Client
}

func NewClient(tr *http.Transport) *VClient {
	httpClient := &http.Client{Transport: tr}
	return &VClient{
		client: httpClient,
	}
}

func (vc *VClient) Post(endpoint string, body any) (*http.Response, error) {
	// TODO body
	req, err := http.NewRequest("POST", baseUrl+endpoint, nil)
	if err != nil {
		return nil, err
	}

	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", GetCurrentToken())
	return vc.client.Do(req)
}

func (vc *VClient) StartBackground() error {
	wakeChan, err := iokit.MonitorSleepWake()
	if err != nil {
		return err
	}

	go func() {
		for {
			<-wakeChan
			fmt.Println("sync req")
			go func() {
				_, err := vc.Post("time/sync", nil)
				if err != nil {
					fmt.Println("sync err:", err)
				}
				fmt.Println("r done")
			}()
		}
	}()

	return nil
}
