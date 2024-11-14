package wormclient

import (
	"fmt"
	"os"

	pb "github.com/orbstack/macvirt/scon/cmd/scli/generated"
	"github.com/orbstack/macvirt/scon/cmd/scli/scli"
	"github.com/orbstack/macvirt/scon/cmd/scli/spinutil"
	"github.com/orbstack/macvirt/vmgr/dockerclient"
)

func nukeLocalData() error {
	scli.EnsureSconVMWithSpinner()

	spinner := spinutil.Start("blue", "Resetting Debug Shell data")
	defer spinner.Stop()

	return scli.Client().WormholeNukeData()
}

func nukeRemoteData(daemon *dockerclient.DockerConnection, drmToken string) error {
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
		ClientMessage: &pb.RpcClientMessage_NukeData{},
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
		return nukeLocalData()
	}

	entitlementToken, err := GetDrmToken()
	if err != nil {
		return err
	}
	return nukeRemoteData(daemon, entitlementToken)
}
