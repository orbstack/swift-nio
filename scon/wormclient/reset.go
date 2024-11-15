package wormclient

import (
	"fmt"
	"os"

	"github.com/orbstack/macvirt/scon/cmd/scli/scli"
	"github.com/orbstack/macvirt/scon/cmd/scli/spinutil"
	pb "github.com/orbstack/macvirt/scon/wormclient/generated"
	"github.com/orbstack/macvirt/vmgr/dockerclient"
)

func resetLocalData() error {
	scli.EnsureSconVMWithSpinner()

	spinner := spinutil.Start("blue", "Resetting Debug Shell data")
	defer spinner.Stop()

	return scli.Client().WormholeNukeData()
}

func resetRemoteData(daemon *dockerclient.DockerConnection, drmToken string) error {
	spinner := spinutil.Start("blue", "Resetting (Remote) Debug Shell data")
	defer spinner.Stop()

	client, err := dockerclient.NewClientWithDrmAuth(daemon, drmToken)
	if err != nil {
		return err
	}

	server, err := connectRemote(client, drmToken, maxRetries)
	if err != nil {
		return err
	}

	// todo: with rpc, directly send NukeData request and get response back
	server.WriteMessage(&pb.RpcClientMessage{
		ClientMessage: &pb.RpcClientMessage_ResetData{},
	})
	message := &pb.RpcServerMessage{}
	if err := server.ReadMessage(message); err != nil {
		return err
	}
	var exitCode int
	switch v := message.ServerMessage.(type) {
	case *pb.RpcServerMessage_ExitStatus:
		exitCode = int(v.ExitStatus.ExitCode)
	}

	if exitCode == 1 {
		fmt.Fprintf(os.Stderr, "Please exit all Debug Shell sessions before using this command.")
		os.Exit(1)
	}

	return nil
}
func WormholeReset(context string) (err error) {
	daemon, isLocal, err := GetDaemon(context)
	if err != nil {
		return err
	}

	if isLocal {
		return resetLocalData()
	}

	entitlementToken, err := GetDrmToken()
	if err != nil {
		return err
	}
	return resetRemoteData(daemon, entitlementToken)
}
