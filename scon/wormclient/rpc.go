package wormclient

import (
	"encoding/binary"
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
	if _, err := io.ReadFull(server.reader, lenBytes[:]); err != nil {
		return err
	}
	length := binary.BigEndian.Uint32(lenBytes[:])
	data := make([]byte, length)

	if _, err := io.ReadFull(server.reader, data); err != nil {
		return err
	}
	if err := proto.Unmarshal(data, msg); err != nil {
		return err
	}
	return nil
}
func (server RpcServer) WriteMessage(msg proto.Message) error {
	data, err := proto.Marshal(msg)
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
