package cmd

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"io"
)

type InputMessageType byte
type OutputMessageType byte

const (
	StdinDataType       InputMessageType = 0x01
	TerminalResizeType  InputMessageType = 0x02
	TermiosSettingsType InputMessageType = 0x03
)

const (
	StdDataType OutputMessageType = 0x01
	ExitType    OutputMessageType = 0x02
)

type RpcInputMessage struct {
	Type    InputMessageType
	Payload []byte
}

type RpcOutputMessage struct {
	Type    OutputMessageType
	Payload []byte
}

func DeserializeMessage(reader io.Reader) (RpcOutputMessage, error) {
	var typeByte [1]byte
	if _, err := io.ReadFull(reader, typeByte[:]); err != nil {
		return RpcOutputMessage{}, fmt.Errorf("failed to read RPC type: %w", err)
	}
	rpcType := OutputMessageType(typeByte[0])

	var data []byte

	if rpcType == StdDataType {

		var lenBytes [4]byte
		if _, err := io.ReadFull(reader, lenBytes[:]); err != nil {
			return RpcOutputMessage{}, fmt.Errorf("failed to read length: %w", err)
		}
		length := binary.BigEndian.Uint32(lenBytes[:])
		data = make([]byte, length)

	} else if rpcType == ExitType {
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
		Type:    StdinDataType,
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
		Type:    TerminalResizeType,
		Payload: buf.Bytes(),
	}, nil
}

func RpcTerminalResize(writer io.Writer, width, height int) error {
	writer.Write([]byte{byte(TerminalResizeType)})
	if err := binary.Write(writer, binary.BigEndian, uint16(width)); err != nil {
		return err
	}
	if err := binary.Write(writer, binary.BigEndian, uint16(height)); err != nil {
		return err
	}
	return nil
}
