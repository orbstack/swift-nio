package wormclient

import (
	"encoding/binary"
	"fmt"
	"io"

	"google.golang.org/protobuf/proto"
)

//go:generate protoc --proto_path=../../wormhole/schema --go_out=. ../../wormhole/schema/wormhole.proto

type RpcServer struct {
	reader io.ReadCloser
	writer io.WriteCloser
}

// todo: refactor
func (server RpcServer) ReadMessage(msg proto.Message) error {
	var lenBytes [4]byte
	_, err := io.ReadFull(server.reader, lenBytes[:])
	if err != nil {
		return fmt.Errorf("failed to read message length: %w", err)
	}
	length := binary.BigEndian.Uint32(lenBytes[:])
	data := make([]byte, length)

	_, err = io.ReadFull(server.reader, data)
	if err != nil {
		return fmt.Errorf("failed to read rpc message: %w", err)
	}
	err = proto.Unmarshal(data, msg)
	if err != nil {
		return fmt.Errorf("failed to unmarshal rpc message: %w", err)
	}

	return nil
}

func (server RpcServer) WriteMessage(msg proto.Message) error {
	data, err := proto.Marshal(msg)
	if err != nil {
		return fmt.Errorf("failed to marshal rpc message: %w", err)
	}

	err = binary.Write(server.writer, binary.BigEndian, uint32(len(data)))
	if err != nil {
		return fmt.Errorf("failed to write message length: %w", err)
	}
	_, err = server.writer.Write(data)
	if err != nil {
		return fmt.Errorf("failed to write rpc message: %w", err)
	}

	return nil
}
