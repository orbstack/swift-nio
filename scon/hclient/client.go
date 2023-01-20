package hclient

import "net/rpc"

type Client struct {
	r *rpc.Client
}

func New() *Client {

	r := rpc.NewClient(nil)
	return &Client{}
}
