package sysnet

import (
	"encoding/hex"
	"errors"
	"net"
	"net/netip"
	"os"
	"strconv"
	"strings"
)

const (
	cTCPListen = 10
	cUDPListen = 7 // ??? don't know where this comes from

	ProtoTCP  = "tcp"
	protoTCP6 = "tcp6"
	ProtoUDP  = "udp"
	protoUDP6 = "udp6"
)

var allProtos = []string{ProtoTCP, protoTCP6, ProtoUDP, protoUDP6}

type ProcListener struct {
	Addr  netip.Addr
	Port  uint16
	Proto string
}

func (p *ProcListener) String() string {
	return p.Proto + "://" + net.JoinHostPort(p.Addr.String(), strconv.Itoa(int(p.Port)))
}

func (p *ProcListener) HostListenIP() string {
	if p.Addr.Is4() {
		if p.Addr.IsLoopback() {
			return "127.0.0.1"
		}
		return "0.0.0.0"
	}

	// IPv6
	if p.Addr.IsLoopback() {
		return "::1"
	}
	return "::"
}

func parseHexAddr(addr string) (net.IP, uint16, error) {
	fields := strings.Split(addr, ":")
	if len(fields) != 2 {
		return nil, 0, errors.New("invalid address")
	}

	// slice
	addrBytes, err := hex.DecodeString(fields[0])
	if err != nil {
		return nil, 0, err
	}
	// byte swap chunks of 4
	for i := 0; i < len(addrBytes); i += 4 {
		addrBytes[i], addrBytes[i+3] = addrBytes[i+3], addrBytes[i]
		addrBytes[i+1], addrBytes[i+2] = addrBytes[i+2], addrBytes[i+1]
	}

	port, err := strconv.ParseUint(fields[1], 16, 16)
	if err != nil {
		return nil, 0, err
	}

	return net.IP(addrBytes), uint16(port), nil
}

func parseProcNet(data string, proto string) ([]ProcListener, error) {
	listeners := make([]ProcListener, 0)
	lines := strings.Split(data, "\n")[1:] // skip header

	expectState := cTCPListen
	if proto == ProtoUDP || proto == protoUDP6 {
		expectState = cUDPListen
	}

	for _, line := range lines {
		if line == "" {
			continue
		}

		// sl  local_address rem_address   st tx_queue rx_queue tr tm->when retrnsmt   uid  timeout inode
		fields := strings.Fields(line)

		// check state first to avoid parsing all
		state, err := strconv.ParseUint(fields[3], 16, 16)
		if err != nil {
			return nil, err
		}
		if int(state) != expectState {
			continue
		}

		// remote addr must be unbound
		remoteAddrPort := fields[2]
		if remoteAddrPort != "00000000:0000" && remoteAddrPort != "00000000000000000000000000000000:0000" {
			continue
		}

		localAddr, localPort, err := parseHexAddr(fields[1])
		if err != nil {
			return nil, err
		}
		localNetip, ok := netip.AddrFromSlice(localAddr)
		if !ok {
			return nil, errors.New("invalid address")
		}

		// exclude zero port
		if localPort == 0 {
			continue
		}

		// don't care about remote addr - it's always 00000000:0000
		listeners = append(listeners, ProcListener{
			Addr:  localNetip,
			Port:  localPort,
			Proto: strings.TrimSuffix(proto, "6"),
		})
	}

	return listeners, nil
}

func ReadAllProcNet(pid string) ([]ProcListener, error) {
	var listeners []ProcListener

	for _, proto := range allProtos {
		data, err := os.ReadFile("/proc/" + pid + "/net/" + proto)
		if err != nil {
			return nil, err
		}

		newListeners, err := parseProcNet(string(data), proto)
		if err != nil {
			return nil, err
		}
		listeners = append(listeners, newListeners...)
	}

	return listeners, nil
}
