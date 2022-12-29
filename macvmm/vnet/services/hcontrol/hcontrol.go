package hcsrv

import (
	"crypto/rand"
	"encoding/base32"
	"net/http"

	"github.com/kdrag0n/macvirt/macvmm/vnet/gonet"
	"github.com/sirupsen/logrus"
	"gvisor.dev/gvisor/pkg/tcpip"
	"gvisor.dev/gvisor/pkg/tcpip/network/ipv4"
	"gvisor.dev/gvisor/pkg/tcpip/stack"
)

const (
	HcontrolPort = 8300
)

var (
	instanceToken = genToken()
)

func genToken() string {
	buf := make([]byte, 32)
	_, err := rand.Read(buf)
	if err != nil {
		panic(err)
	}

	// to base32
	b32str := base32.StdEncoding.WithPadding(base32.NoPadding).EncodeToString(buf)
	return b32str
}

func GetCurrentToken() string {
	return instanceToken
}

func ListenHcontrol(stack *stack.Stack, address tcpip.Address) error {
	mux := http.NewServeMux()
	mux.HandleFunc("/ping", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})

	listener, err := gonet.ListenTCP(stack, tcpip.FullAddress{
		Addr: address,
		Port: HcontrolPort,
	}, ipv4.ProtocolNumber)
	if err != nil {
		return err
	}

	server := &http.Server{
		Handler: mux,
	}
	go func() {
		err := server.Serve(listener)
		if err != nil {
			logrus.Error("hcontrol: Serve() =", err)
		}
	}()

	return nil
}
