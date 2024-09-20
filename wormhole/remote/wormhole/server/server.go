package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net"
	"os"
	"os/exec"

	pb "github.com/orbstack/macvirt/wormhole/remote/wormhole/protobuf"
	"golang.org/x/sys/unix"
	"google.golang.org/grpc"
)

type server struct {
	pb.UnimplementedWormholeServiceServer
}

type WormholeConfig struct {
	InitPid             int    `json:"init_pid"`
	WormholeMountTreeFd int    `json:"wormhole_mount_tree_fd"`
	ExitCodePipeWriteFd int    `json:"exit_code_pipe_write_fd"`
	LogFd               int    `json:"log_fd"`
	DrmToken            string `json:"drm_token"`

	ContainerWorkdir string   `json:"container_workdir,omitempty"`
	ContainerEnv     []string `json:"container_env"`

	EntryShellCmd string `json:"entry_shell_cmd,omitempty"`
}

func SpawnWormholeAttach() (io.WriteCloser, io.ReadCloser, *exec.Cmd, error) {
	wormholeMountFd, err := unix.OpenTree(unix.AT_FDCWD, "/mnt/wormhole-unified/nix", unix.OPEN_TREE_CLOEXEC|unix.OPEN_TREE_CLONE|unix.AT_RECURSIVE)
	if err != nil {
		return nil, nil, nil, err
	}
	wormholeMountFile := os.NewFile(uintptr(wormholeMountFd), "wormhole mount")

	defer wormholeMountFile.Close()

	exitCodePipeRead, exitCodePipeWrite, err := os.Pipe()
	if err != nil {
		return nil, nil, nil, err
	}

	logPipeRead, logPipeWrite, err := os.Pipe()
	if err != nil {
		return nil, nil, nil, err
	}

	go io.Copy(os.Stderr, logPipeRead)
	config := &WormholeConfig{
		InitPid: 700,
		// wormholeMountFile = dup to fd 3 in child
		WormholeMountTreeFd: 3,
		ExitCodePipeWriteFd: 4,
		LogFd:               5,
		DrmToken:            "eyJhbGciOiJFZERTQSIsImtpZCI6IjEiLCJ0eXAiOiJKV1QifQ.eyJzdWIiOiIiLCJlbnQiOjEsImV0cCI6MiwiZW1nIjpudWxsLCJlc3QiOm51bGwsImF1ZCI6Im1hY3ZpcnQiLCJ2ZXIiOnsiY29kZSI6MTA3MDEwMCwiZ2l0IjoiMmUzZjdlZWVhNjQ0NWEyZjZlYWI1MzM0MTkzNjBkZmU2NmZiODNkYSJ9LCJkaWQiOiI3YmE5ZjA1ZDBlMGY2NTI3MjVkYzA3NjM5Y2VmYTg2NTM2ZWVlMmU5NTc4NDk2OWVlODcwZWMyZDY2YjEzMDI0IiwiaWlkIjoiYzdlYzY1M2FmZDljMDIxNjZlZjY2Nzc2MGVkYWNmODA0ZDc4OTlhZDE3YmQ1YWIxYzU4YzE4OGVjOGYxZTExYiIsImNpZCI6ImU1NjZiZjRiNmExNjNjYTM1NGU2OGQzYmU2ZjAzZDlmNzFkMzYxZTdhMmIxNjMzZDcwMzE0MmE2ODIwNmNjNDciLCJpc3MiOiJkcm1zZXJ2ZXIiLCJpYXQiOjE3MjY2ODQyMjUsImV4cCI6MTcyNzI4OTAyNSwibmJmIjoxNzI2Njg0MjI1LCJkdnIiOjEsIndhciI6MTcyNjk3MTM3MiwibHhwIjoxNzI3NTc2MTcyfQ.asnYZORqAuIxyuusi8GVLql6GzF3oSEyyTJnQDw2F4FE11mRAJGWWm6wVWaphnyQUYptTmDvbp3VeRBg0HWGAw",

		// instead of launching wormhole-attach process with the container's env, we pass it separately because there are several env priorities:
		// 1. start with container env (* from scon)
		// 2. override with pid 1 env
		// 3. override with required wormhole env
		// 4. override with TERM, etc. (* from scon)
		// #1 and #4 are both from scon, so must be separate
		// ContainerWorkdir: workDir,
		// ContainerEnv:     wormholeResp.Env,

		// EntryShellCmd: ,
	}
	configBytes, err := json.Marshal(config)
	if err != nil {
		return nil, nil, nil, err
	}
	fmt.Println("config bytes: ", string(configBytes))

	// defer file.Close()
	// cmd := exec.Command("./wormhole-attach", string(configBytes))
	cmd := exec.Command("ls", "-l", "/proc/self/fd")
	cmd.Env = append(os.Environ(), "RUST_BACKTRACE=full")
	cmd.ExtraFiles = []*os.File{wormholeMountFile, exitCodePipeWrite, logPipeWrite}

	attachStdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, nil, nil, fmt.Errorf("could not make stdin pipe: %v", err)
	}
	attachStdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, nil, nil, fmt.Errorf("could not make stdout pipe: %v", err)
	}

	statusBytes := make([]byte, 1) // exit codes only range from 0-255 so it should be able to fit into a single byte
	n, err := exitCodePipeRead.Read(statusBytes)
	if err != nil {
		return nil, nil, nil, err
	}
	if n < 1 {
		err = fmt.Errorf("could not read exitcode from pipe")
		return nil, nil, nil, err
	}
	fmt.Println("status code: ", statusBytes)

	return attachStdin, attachStdout, cmd, nil
}

func (s *server) SendCommand(stream pb.WormholeService_SendCommandServer) error {
	wormholeMountFd, err := unix.OpenTree(unix.AT_FDCWD, "/mnt/wormhole-unified/nix", unix.OPEN_TREE_CLOEXEC|unix.OPEN_TREE_CLONE|unix.AT_RECURSIVE)
	if err != nil {
		return err
	}
	wormholeMountFile := os.NewFile(uintptr(wormholeMountFd), "wormhole mount")

	exitCodePipeRead, exitCodePipeWrite, err := os.Pipe()
	if err != nil {
		return err
	}

	logPipeRead, logPipeWrite, err := os.Pipe()
	if err != nil {
		return err
	}

	defer func() {
		wormholeMountFile.Close()
		exitCodePipeRead.Close()
		exitCodePipeWrite.Close()
		logPipeRead.Close()
		logPipeWrite.Close()
	}()

	go io.Copy(os.Stderr, logPipeRead)
	config := &WormholeConfig{
		InitPid: 4780,
		// wormholeMountFile = dup to fd 3 in child
		WormholeMountTreeFd: 3,
		ExitCodePipeWriteFd: 4,
		LogFd:               5,
		DrmToken:            "eyJhbGciOiJFZERTQSIsImtpZCI6IjEiLCJ0eXAiOiJKV1QifQ.eyJzdWIiOiIiLCJlbnQiOjEsImV0cCI6MiwiZW1nIjpudWxsLCJlc3QiOm51bGwsImF1ZCI6Im1hY3ZpcnQiLCJ2ZXIiOnsiY29kZSI6MTA3MDEwMCwiZ2l0IjoiMmUzZjdlZWVhNjQ0NWEyZjZlYWI1MzM0MTkzNjBkZmU2NmZiODNkYSJ9LCJkaWQiOiI3YmE5ZjA1ZDBlMGY2NTI3MjVkYzA3NjM5Y2VmYTg2NTM2ZWVlMmU5NTc4NDk2OWVlODcwZWMyZDY2YjEzMDI0IiwiaWlkIjoiYzdlYzY1M2FmZDljMDIxNjZlZjY2Nzc2MGVkYWNmODA0ZDc4OTlhZDE3YmQ1YWIxYzU4YzE4OGVjOGYxZTExYiIsImNpZCI6ImU1NjZiZjRiNmExNjNjYTM1NGU2OGQzYmU2ZjAzZDlmNzFkMzYxZTdhMmIxNjMzZDcwMzE0MmE2ODIwNmNjNDciLCJpc3MiOiJkcm1zZXJ2ZXIiLCJpYXQiOjE3MjY2ODQyMjUsImV4cCI6MTcyNzI4OTAyNSwibmJmIjoxNzI2Njg0MjI1LCJkdnIiOjEsIndhciI6MTcyNjk3MTM3MiwibHhwIjoxNzI3NTc2MTcyfQ.asnYZORqAuIxyuusi8GVLql6GzF3oSEyyTJnQDw2F4FE11mRAJGWWm6wVWaphnyQUYptTmDvbp3VeRBg0HWGAw",

		// instead of launching wormhole-attach process with the container's env, we pass it separately because there are several env priorities:
		// 1. start with container env (* from scon)
		// 2. override with pid 1 env
		// 3. override with required wormhole env
		// 4. override with TERM, etc. (* from scon)
		// #1 and #4 are both from scon, so must be separate
		// ContainerWorkdir: workDir,
		// ContainerEnv:     wormholeResp.Env,

		// EntryShellCmd: ,
	}
	configBytes, err := json.Marshal(config)
	if err != nil {
		return err
	}
	fmt.Println("config bytes: ", string(configBytes))

	// defer file.Close()
	cmd := exec.Command("./wormhole-attach", string(configBytes))
	// cmd := exec.Command("ls", "-l", "/proc/self/fd")
	cmd.Env = append(os.Environ(), "RUST_BACKTRACE=full")
	cmd.ExtraFiles = []*os.File{wormholeMountFile, exitCodePipeWrite, logPipeWrite}

	attachStdin, err := cmd.StdinPipe()
	if err != nil {
		return fmt.Errorf("could not make stdin pipe: %v", err)
	}
	attachStdout, err := cmd.StdoutPipe()
	if err != nil {
		return fmt.Errorf("could not make stdout pipe: %v", err)
	}

	fmt.Println("running cmd")

	go func() {
		reader := bufio.NewReader(attachStdout)
		buffer := make([]byte, 1024)
		fmt.Println("reading")
		for {
			n, err := reader.Read(buffer)
			if err == io.EOF {
				break
			}
			if err != nil {
				fmt.Printf("error reading wormhole-attach output: %v", err)
				return
			}
			stream.Send(&pb.OutputMessage{Output: string(buffer[:n])})
		}
	}()

	go func() {
		for {
			in, err := stream.Recv()
			if err == io.EOF {
				break
			}
			if err != nil {
				fmt.Println("error when receiving from client: ", err)
				return
			}
			fmt.Println("running command " + in.Input)
			io.WriteString(attachStdin, in.Input)
		}
	}()

	if err := cmd.Start(); err != nil {
		return fmt.Errorf("could not start command: %v", err)
	}

	// add err channel
	statusBytes := make([]byte, 1) // exit codes only range from 0-255 so it should be able to fit into a single byte
	n, err := exitCodePipeRead.Read(statusBytes)
	if err != nil {
		return err
	}
	if n < 1 {
		err = fmt.Errorf("could not read exitcode from pipe")
		return err
	}
	fmt.Println("status code: ", statusBytes)

	return nil
}

func main() {
	lis, err := net.Listen("tcp", ":50051")
	if err != nil {
		log.Fatalf("Failed to listen: %v", err)
	}

	s := grpc.NewServer()
	pb.RegisterWormholeServiceServer(s, &server{})

	fmt.Println("Server starting")
	if err := s.Serve(lis); err != nil {
		log.Fatalf("Failed to server: %v", err)
	}
}
