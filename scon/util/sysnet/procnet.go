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

	protoTCP6 = "tcp6"
	protoUDP6 = "udp6"
)

var allProcProtos = []string{string(ProtoTCP), protoTCP6, string(ProtoUDP), protoUDP6}

type TransportProtocol string

const (
	ProtoUDP TransportProtocol = "udp"
	ProtoTCP TransportProtocol = "tcp"
)

type ListenerInfo struct {
	ListenerKey

	// if it's docker forward or k8s
	FromIptables bool
	// optional: override for macOS side
	ExtListenAddr netip.Addr
}

func (i ListenerInfo) UseIptables() bool {
	// all wildcard listeners *could* use iptables, because we preserve source IP and translate getpeername in cfwd
	// but in reality, that causes some issues:
	// Docker's default port forwarding rules do DNAT but not MASQUERADE or SNAT. This preserves source IP and it works because return path goes through host netns' default route.
	// We can fix this in our managed docker machine by adding MASQUERADE rules by source IP, but it's not possible to do this for whatever docker or k8s people may be running in machines.
	// if the 198.19 IP goes through, it gets translated to localhost *inside* the container, which is unexpected as it should've been the host's IP.
	// so disable it until we can do true localhost-like forwarding with raw bpf skbs.
	return i.FromIptables //|| i.AddrPort.Addr().IsUnspecified()
}

func (i *ListenerInfo) Identifier() ListenerKey {
	return i.ListenerKey
}

type ListenerKey struct {
	netip.AddrPort
	Proto TransportProtocol
}

func (k ListenerKey) String() string {
	return string(k.Proto) + ":" + k.AddrPort.String()
}

func (i ListenerInfo) HostListenIP() string {
	// prefer ExtListenAddr
	if i.ExtListenAddr.IsValid() {
		return i.ExtListenAddr.String()
	}

	if i.Addr().Is4() {
		if i.Addr().IsLoopback() {
			return "127.0.0.1"
		}
		return "0.0.0.0"
	}

	// IPv6
	if i.Addr().IsLoopback() {
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

func parseProcNet(data string, proto string) ([]ListenerInfo, error) {
	listeners := make([]ListenerInfo, 0)
	lines := strings.Split(data, "\n")[1:] // skip header

	expectState := cTCPListen
	if proto == string(ProtoUDP) || proto == protoUDP6 {
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
		listeners = append(listeners, ListenerInfo{
			ListenerKey: ListenerKey{
				AddrPort: netip.AddrPortFrom(localNetip, localPort),
				Proto:    TransportProtocol(strings.TrimSuffix(proto, "6")),
			},
		})
	}

	return listeners, nil
}

func ReadAllProcNet(pid string) ([]ListenerInfo, error) {
	var listeners []ListenerInfo

	for _, proto := range allProcProtos {
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
