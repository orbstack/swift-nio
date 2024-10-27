package cmd

import (
	"encoding/binary"
	"io"

	"golang.org/x/sys/unix"
)

type RpcInputType byte
type RpcOutputType byte

const (
	ReadStdinType    RpcInputType = 0x01
	WindowChangeType RpcInputType = 0x02
	RequestPtyType   RpcInputType = 0x03
	StartType        RpcInputType = 0x04
	StartServerAck   RpcInputType = 0x5
)

const (
	ReadStdioType     RpcOutputType = 0x01
	StartServerType   RpcOutputType = 0x02
	ExitCodeType      RpcOutputType = 0x03
	ConnectServerType RpcInputType  = 0x04
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

func (server RpcServer) writeBytes(data []byte) error {
	if err := binary.Write(server.writer, binary.BigEndian, uint32(len(data))); err != nil {
		return err
	}
	if _, err := server.writer.Write(data); err != nil {
		return err
	}
	return nil

}

func (server RpcServer) RpcWindowChange(h, w int) error {
	if _, err := server.writer.Write([]byte{byte(WindowChangeType)}); err != nil {
		return err
	}
	if err := binary.Write(server.writer, binary.BigEndian, uint16(h)); err != nil {
		return err
	}
	if err := binary.Write(server.writer, binary.BigEndian, uint16(w)); err != nil {
		return err
	}
	return nil
}

func (server RpcServer) RpcWriteStdin(data []byte) error {
	if _, err := server.writer.Write([]byte{byte(ReadStdinType)}); err != nil {
		return err
	}
	if err := server.writeBytes(data); err != nil {
		return err
	}
	return nil
}

func (server RpcServer) RpcStartServerAck() error {
	if _, err := server.writer.Write([]byte{byte(StartServerAck)}); err != nil {
		return err
	}
	return nil
}

func (server RpcServer) RpcRequestPty(termEnv string, h, w int, termios *unix.Termios) error {
	if _, err := server.writer.Write([]byte{byte(RequestPtyType)}); err != nil {
		return err
	}
	// send termenv, height (rows), width (cols), termios
	if err := server.writeBytes([]byte(termEnv)); err != nil {
		return err
	}
	if err := binary.Write(server.writer, binary.BigEndian, uint16(h)); err != nil {
		return err
	}
	if err := binary.Write(server.writer, binary.BigEndian, uint16(w)); err != nil {
		return err
	}
	termiosConfig, err := SerializeTermios(termios)
	if err != nil {
		return err
	}
	if err := server.writeBytes(termiosConfig); err != nil {
		return err
	}
	return nil
}

func (server RpcServer) RpcStart(wormholeParams []byte) error {
	_, err := server.writer.Write([]byte{byte(StartType)})
	if err := server.writeBytes(wormholeParams); err != nil {
		return err
	}
	return err
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
