package cmd

import (
	"encoding/binary"
	"io"
)

type RpcInputType byte
type RpcOutputType byte

const (
	WriteStdinType     RpcInputType = 0x01
	ResizeTerminalType RpcInputType = 0x02
	InitTermiosType    RpcInputType = 0x03
)

const (
	ReadStdioType RpcOutputType = 0x01
	ExitCodeType  RpcOutputType = 0x02
)

type RpcInputMessage struct {
	Type    RpcInputType
	Payload []byte
}

type RpcOutputMessage struct {
	Type    RpcOutputType
	Payload []byte
}

type RpcServer struct {
	reader io.ReadCloser
	writer io.WriteCloser
}

func (server RpcServer) Start() error {
	return nil
}

func (server RpcServer) RpcResizeTerminal(width, height int) error {
	if _, err := server.writer.Write([]byte{byte(ResizeTerminalType)}); err != nil {
		return err
	}
	if err := binary.Write(server.writer, binary.BigEndian, uint16(width)); err != nil {
		return err
	}
	if err := binary.Write(server.writer, binary.BigEndian, uint16(height)); err != nil {
		return err
	}
	return nil
}

func (server RpcServer) RpcWriteStdin(data []byte) error {
	if _, err := server.writer.Write([]byte{byte(WriteStdinType)}); err != nil {
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

func (server RpcServer) RpcRead() (RpcOutputType, []byte, error) {
	var typeByte [1]byte
	if _, err := io.ReadFull(server.reader, typeByte[:]); err != nil {
		return 0, nil, err
	}
	rpcType := RpcOutputType(typeByte[0])

	var data []byte
	if rpcType == ReadStdioType {
		var lenBytes [4]byte
		if _, err := io.ReadFull(server.reader, lenBytes[:]); err != nil {
			return 0, nil, err
		}
		length := binary.BigEndian.Uint32(lenBytes[:])
		data = make([]byte, length)

	} else if rpcType == ExitCodeType {
		data = make([]byte, 1)
	}

	if _, err := io.ReadFull(server.reader, data); err != nil {
		return 0, nil, err
	}
	return rpcType, data, nil

}
