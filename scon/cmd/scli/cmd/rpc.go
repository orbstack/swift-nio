package cmd

import (
	"encoding/binary"
	"io"

	pb "github.com/orbstack/macvirt/scon/cmd/scli/generated"
	"google.golang.org/protobuf/proto"
)

type RpcServer struct {
	reader io.ReadCloser
	writer io.WriteCloser
}

func (server RpcServer) ReadMessage() (*pb.RpcServerMessage, error) {
	var lenBytes [4]byte
	if _, err := io.ReadFull(server.reader, lenBytes[:]); err != nil {
		return nil, err
	}
	length := binary.BigEndian.Uint32(lenBytes[:])
	data := make([]byte, length)

	if _, err := io.ReadFull(server.reader, data); err != nil {
		return nil, err
	}

	message := &pb.RpcServerMessage{}
	if err := proto.Unmarshal(data, message); err != nil {
		return nil, err
	}

	return message, nil
}

func (server RpcServer) WriteMessage(message *pb.RpcClientMessage) error {
	data, err := proto.Marshal(message)
	if err != nil {
		return err
	}

	if err := binary.Write(server.writer, binary.BigEndian, uint32(len(data))); err != nil {
		return err
	}
	if _, err := server.writer.Write(data); err != nil {
		return err
	}
	return nil
}
