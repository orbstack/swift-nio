package main

import (
	"encoding/binary"
	"encoding/hex"
	"fmt"
	"io"
	"math"
	"net"
	"os"
	"strconv"
	"strings"

	"github.com/orbstack/macvirt/scon/util/sysx"
	"github.com/orbstack/macvirt/vmgr/conf/ports"
	"github.com/orbstack/macvirt/vmgr/vnet/netconf"
	"github.com/sirupsen/logrus"
	"golang.org/x/sys/unix"
)

const nfsdRpcBufSize = 16384

const (
	// mappings to /etc/exports
	NFSEXP_READONLY        = 0x0001  // ro / rw
	NFSEXP_INSECURE_PORT   = 0x0002  // insecure
	NFSEXP_ROOTSQUASH      = 0x0004  // root_squash / no_root_squash (default??)
	NFSEXP_ALLSQUASH       = 0x0008  // all_squash
	NFSEXP_ASYNC           = 0x0010  // async / sync
	NFSEXP_GATHERED_WRITES = 0x0020  // wdelay / no_wdelay (default=1)
	NFSEXP_NOREADDIRPLUS   = 0x0040  // nordirplus
	NFSEXP_SECURITY_LABEL  = 0x0080  // security_label
	NFSEXP_NOHIDE          = 0x0200  // nohide
	NFSEXP_NOSUBTREECHECK  = 0x0400  // no_subtree_check
	NFSEXP_NOAUTHNLM       = 0x0800  // insecure_locks
	NFSEXP_MSNFS           = 0x1000  // (resereved)
	NFSEXP_FSID            = 0x2000  // (has fsid=X)
	NFSEXP_CROSSMOUNT      = 0x4000  // crossmnt
	NFSEXP_NOACL           = 0x8000  // (reserved)
	NFSEXP_V4ROOT          = 0x10000 // v4root
	NFSEXP_PNFS            = 0x20000 // pnfs
	// custom
	NFSEXP_QFID = 0x40000 // qfid

	// rw,async,fsid=0,crossmnt,insecure,all_squash,no_subtree_check
	// parts that vary: ro,fsid=%d,anonuid=%d,anongid=%d
	nfsExpBaseFlags = NFSEXP_INSECURE_PORT | NFSEXP_ROOTSQUASH | NFSEXP_ALLSQUASH | NFSEXP_ASYNC | NFSEXP_GATHERED_WRITES | NFSEXP_NOSUBTREECHECK | NFSEXP_FSID | NFSEXP_CROSSMOUNT | NFSEXP_QFID

	nfsExpSecinfoFlags = NFSEXP_READONLY | NFSEXP_ROOTSQUASH | NFSEXP_ALLSQUASH | NFSEXP_INSECURE_PORT

	// the only fsid type we use
	FSID_NUM = 1

	nfsExportRoot    = "/nfs/root/ro"
	nfsAllowedClient = netconf.VnetGatewayIP4

	nfsdListenAddr = netconf.VnetGuestIP4
	nfsdThreads    = 8
)

type nfsExportEntry struct {
	flags   uint32
	anonUid int
	anonGid int
	fsid    uint32
}

func startNfsd() error {
	// only nfs 4
	err := os.WriteFile("/proc/fs/nfsd/versions", []byte("-2 -3 +4 +4.1 +4.2\n"), 0)
	if err != nil {
		return err
	}

	// listen on IPv4 port 2049 on vnet interface, nonblock off
	listener, err := net.Listen("tcp", nfsdListenAddr+":"+strconv.Itoa(ports.GuestNFS))
	if err != nil {
		return err
	}

	listenerFile, err := listener.(*net.TCPListener).File()
	listener.Close() // dup
	if err != nil {
		return err
	}
	defer listenerFile.Close()

	// write fd to /proc/fs/nfsd/portlist
	err = os.WriteFile("/proc/fs/nfsd/portlist", []byte(strconv.Itoa(int(listenerFile.Fd()))), 0)
	if err != nil {
		return err
	}

	// start with 8 threads
	err = os.WriteFile("/proc/fs/nfsd/threads", []byte(strconv.Itoa(nfsdThreads)), 0)
	if err != nil {
		return err
	}

	return nil
}

func serveAuthUnixIp() error {
	file, err := os.OpenFile("/proc/net/rpc/auth.unix.ip/channel", os.O_RDWR, 0)
	if err != nil {
		return err
	}
	defer file.Close()

	var buf [nfsdRpcBufSize]byte
	for {
		err = sysx.PollFd(int(file.Fd()), unix.POLLIN)
		if err != nil {
			return err
		}

		// guaranteed to read a single line in a call
		n, err := file.Read(buf[:])
		if err != nil {
			return err
		}
		if n == 0 {
			return io.EOF
		}

		line := string(buf[:n])
		var reqClass string
		var reqIP string
		n, err = fmt.Sscanf(line, "%s %s\n", &reqClass, &reqIP)
		if err != nil {
			return err
		}
		if n != 2 {
			return fmt.Errorf("invalid line: %s", line)
		}
		if reqClass != "nfsd" {
			continue
		}

		// write IP back, don't resolve domain
		// must be in one line
		expiryTime := int64(math.MaxInt64)
		var outStr string
		if reqIP == nfsAllowedClient {
			outStr = fmt.Sprintf("nfsd %s %d %s\n", reqIP, expiryTime, reqIP)
		} else {
			outStr = fmt.Sprintf("nfsd %s %d\n", reqIP, expiryTime)
		}
		_, err = file.WriteString(outStr)
		if err != nil {
			return err
		}
	}
}

func (m *NfsMirrorManager) handleNfsdExport(reqDomain string, reqPath string) string {
	m.mu.Lock()
	defer m.mu.Unlock()

	expiryTime := int64(math.MaxInt64)
	exp, ok := m.exports[reqPath]
	if !ok {
		// can't find export = return error
		logrus.WithFields(logrus.Fields{
			"reqDomain": reqDomain,
			"reqPath":   reqPath,
		}).Error("nfs export not found")
		return fmt.Sprintf("%s %s %d\n", reqDomain, nfsPathToHex(reqPath), expiryTime)
	}

	// secinfo: 1 item, sec=sys (= AUTH_UNIX = 1), flags (should be masked with NFSEXP_SECINFO_FLAGS?)
	return fmt.Sprintf("%s %s %d %d %d %d %d secinfo 1 1 %d\n", reqDomain, nfsPathToHex(reqPath), expiryTime, exp.flags, exp.anonUid, exp.anonGid, exp.fsid, exp.flags)
}

func (m *NfsMirrorManager) serveNfsdExports() error {
	file, err := os.OpenFile("/proc/net/rpc/nfsd.export/channel", os.O_RDWR, 0)
	if err != nil {
		return err
	}
	defer file.Close()

	var buf [nfsdRpcBufSize]byte
	for {
		err = sysx.PollFd(int(file.Fd()), unix.POLLIN)
		if err != nil {
			return err
		}

		// guaranteed to read a single line in a call
		n, err := file.Read(buf[:])
		if err != nil {
			return err
		}
		if n == 0 {
			return io.EOF
		}

		line := string(buf[:n])
		var reqDomain string
		var reqPath string
		// TODO parse path format correctly
		n, err = fmt.Sscanf(line, "%s %s\n", &reqDomain, &reqPath)
		if err != nil {
			return err
		}
		if n != 2 {
			return fmt.Errorf("invalid line: %s", line)
		}

		// write export back
		outStr := m.handleNfsdExport(reqDomain, reqPath)
		_, err = file.WriteString(outStr)
		if err != nil {
			return err
		}
	}
}

func (m *NfsMirrorManager) handleNfsdFh(reqDomain string, reqFsid uint32, reqFsidHex string) string {
	m.mu.Lock()
	defer m.mu.Unlock()

	// scan exports for matching fsid
	expiryTime := int64(math.MaxInt64)
	for path, exp := range m.exports {
		if exp.fsid == reqFsid {
			return fmt.Sprintf("%s %d %s %d %s\n", reqDomain, FSID_NUM, reqFsidHex, expiryTime, nfsPathToHex(path))
		}
	}

	// deny: not found
	return fmt.Sprintf("%s %d %s %d\n", reqDomain, FSID_NUM, reqFsidHex, expiryTime)
}

func (m *NfsMirrorManager) serveNfsdFh() error {
	file, err := os.OpenFile("/proc/net/rpc/nfsd.fh/channel", os.O_RDWR, 0)
	if err != nil {
		return err
	}
	defer file.Close()

	var buf [nfsdRpcBufSize]byte
	for {
		err = sysx.PollFd(int(file.Fd()), unix.POLLIN)
		if err != nil {
			return err
		}

		// guaranteed to read a single line in a call
		n, err := file.Read(buf[:])
		if err != nil {
			return err
		}
		if n == 0 {
			return io.EOF
		}

		line := string(buf[:n])
		var reqDomain string
		var reqFsidType int
		var reqFsidHex string
		n, err = fmt.Sscanf(line, "%s %d %s\n", &reqDomain, &reqFsidType, &reqFsidHex)
		if err != nil {
			return err
		}
		if n != 3 {
			return fmt.Errorf("invalid line: %s", line)
		}
		if reqFsidType != FSID_NUM {
			return fmt.Errorf("invalid fsid type: %d", reqFsidType)
		}
		if !strings.HasPrefix(reqFsidHex, `\x`) {
			return fmt.Errorf("invalid fsid hex: %s", reqFsidHex)
		}
		reqFsidBytes, err := hex.DecodeString(reqFsidHex[2:])
		if err != nil {
			return err
		}
		if len(reqFsidBytes) != 4 {
			return fmt.Errorf("invalid fsid hex length: %d", len(reqFsidBytes))
		}
		reqFsid := binary.LittleEndian.Uint32(reqFsidBytes)

		// scan for fsid
		outStr := m.handleNfsdFh(reqDomain, reqFsid, reqFsidHex)
		_, err = file.WriteString(outStr)
		if err != nil {
			return err
		}
	}
}

// to avoid escaping whitespace
func nfsPathToHex(path string) string {
	return `\x` + hex.EncodeToString([]byte(path))
}
