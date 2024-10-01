package cmd

import (
	"bytes"
	"encoding/binary"
	"fmt"
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

func (server RpcServer) RpcStart() error {
	_, err := server.writer.Write([]byte{byte(StartType)})
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

func DeserializeMessage(reader io.Reader) (RpcOutputMessage, error) {
	var typeByte [1]byte
	if _, err := io.ReadFull(reader, typeByte[:]); err != nil {
		return RpcOutputMessage{}, fmt.Errorf("failed to read RPC type: %w", err)
	}
	rpcType := RpcOutputType(typeByte[0])

	var data []byte

	if rpcType == ReadStdioType {

		var lenBytes [4]byte
		if _, err := io.ReadFull(reader, lenBytes[:]); err != nil {
			return RpcOutputMessage{}, fmt.Errorf("failed to read length: %w", err)
		}
		length := binary.BigEndian.Uint32(lenBytes[:])
		data = make([]byte, length)

	} else if rpcType == ExitCodeType {
		data = make([]byte, 1)
	}

	if _, err := io.ReadFull(reader, data); err != nil {
		return RpcOutputMessage{}, fmt.Errorf("failed to read data: %w", err)
	}
	return RpcOutputMessage{Type: rpcType, Payload: data}, nil
}

func SerializeMessage(msg RpcInputMessage) ([]byte, error) {
	buf := new(bytes.Buffer)
	buf.WriteByte(byte(msg.Type))
	payloadLength := uint32(len(msg.Payload))
	if err := binary.Write(buf, binary.BigEndian, payloadLength); err != nil {
		return nil, err
	}
	buf.Write(msg.Payload)
	return buf.Bytes(), nil
}

func CreateStdinDataMessage(data []byte) RpcInputMessage {
	return RpcInputMessage{
		Type:    ReadStdinType,
		Payload: data,
	}
}

func CreateTerminalResizeMessage(width, height int) (RpcInputMessage, error) {
	buf := new(bytes.Buffer)
	if err := binary.Write(buf, binary.BigEndian, uint32(width)); err != nil {
		return RpcInputMessage{}, err
	}
	if err := binary.Write(buf, binary.BigEndian, uint32(height)); err != nil {
		return RpcInputMessage{}, err
	}
	return RpcInputMessage{
		Type:    WindowChangeType,
		Payload: buf.Bytes(),
	}, nil
}

func RpcTerminalResize(writer io.Writer, width, height int) error {
	writer.Write([]byte{byte(WindowChangeType)})
	if err := binary.Write(writer, binary.BigEndian, uint16(width)); err != nil {
		return err
	}
	if err := binary.Write(writer, binary.BigEndian, uint16(height)); err != nil {
		return err
	}
	return nil
}
