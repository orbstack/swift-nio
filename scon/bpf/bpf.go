package bpf

import (
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net"
	"os"
	"slices"
	"unsafe"

	"github.com/cilium/ebpf"
	"github.com/cilium/ebpf/link"
	"github.com/cilium/ebpf/ringbuf"
	"github.com/sirupsen/logrus"
	"golang.org/x/sys/unix"
)

//go:generate ./build-bpf.sh

const (
	ChildCgroupName = "child"
)

type LtypeFlags uint8

const (
	LtypeTCP LtypeFlags = 1 << iota
	LtypeUDP
	LtypeIPTables

	LtypeAll = LtypeTCP | LtypeUDP | LtypeIPTables
)

type PmonEvent struct {
	DirtyFlags LtypeFlags
}

type CfwdContainerMeta = lfwdCfwdContainerMeta

type ContainerBpfManager struct {
	cgPath      string
	netnsCookie uint64

	closers []io.Closer

	lfwdBlockedPorts *ebpf.Map
	// refcount ports to block
	// keep a port blocked if ANY listeners, v4 OR v6, are using it
	// protected by ctr.mu
	lfwdBlockedPortRefs map[uint16]int

	cfwdNetnsProg      *ebpf.Program
	cfwdHostIps        *ebpf.Map
	cfwdContainerMetas *ebpf.Map
	cfwdAttachedNsKeys map[string]*link.NetNsLink
}

func NewContainerBpfManager(cgPath string, netnsCookie uint64) (*ContainerBpfManager, error) {
	return &ContainerBpfManager{
		cgPath:      cgPath,
		netnsCookie: netnsCookie,

		lfwdBlockedPortRefs: make(map[uint16]int),
		cfwdAttachedNsKeys:  make(map[string]*link.NetNsLink),
	}, nil
}

// called with c.mu held
func (b *ContainerBpfManager) Close() error {
	var errs []error
	for _, c := range b.closers {
		err := c.Close()
		if err != nil {
			errs = append(errs, err)
		}
	}
	for _, l := range b.cfwdAttachedNsKeys {
		err := l.Close()
		if err != nil {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}

// called with c.mu held
func (b *ContainerBpfManager) LfwdBlockPort(port uint16) error {
	if b.lfwdBlockedPorts == nil {
		return nil
	}

	// refcount
	b.lfwdBlockedPortRefs[port]++
	// first ref?
	if b.lfwdBlockedPortRefs[port] == 1 {
		// swap to big endian
		port = (port&0xff)<<8 | (port&0xff00)>>8
		return b.lfwdBlockedPorts.Put(port, byte(1))
	}

	return nil
}

// called with c.mu held
func (b *ContainerBpfManager) LfwdUnblockPort(port uint16) error {
	if b.lfwdBlockedPorts == nil {
		return nil
	}

	// refcount
	b.lfwdBlockedPortRefs[port]--
	// last ref?
	if b.lfwdBlockedPortRefs[port] == 0 {
		// swap to big endian
		port = (port&0xff)<<8 | (port&0xff00)>>8
		return b.lfwdBlockedPorts.Delete(port)
	}

	return nil
}

// called with c.mu held
func (b *ContainerBpfManager) attachOneCg(typ ebpf.AttachType, prog *ebpf.Program) error {
	l, err := link.AttachCgroup(link.CgroupOptions{
		Path:    b.cgPath,
		Attach:  typ,
		Program: prog,
	})
	if err != nil {
		return fmt.Errorf("attach: %w", err)
	}
	b.closers = append(b.closers, l)
	return nil
}

func (b *ContainerBpfManager) attachOneTracing(prog *ebpf.Program) error {
	l, err := link.AttachTracing(link.TracingOptions{
		Program: prog,
	})
	if err != nil {
		return fmt.Errorf("attach: %w", err)
	}
	b.closers = append(b.closers, l)
	return nil
}

// called with c.mu held
func (b *ContainerBpfManager) AttachLfwd() error {
	// must load a new instance to set a different netns cookie in config map
	// maps are per-program instance
	// and this is an unpinned program (no ref in /sys/fs/bpf), so it'll be destroyed
	// when we close fds
	spec, err := loadLfwd()
	if err != nil {
		return fmt.Errorf("load spec: %w", err)
	}

	// set netns cookie filter
	err = spec.RewriteConstants(map[string]any{
		"config_netns_cookie": b.netnsCookie,
	})
	if err != nil {
		return fmt.Errorf("configure: %w", err)
	}

	objs := &lfwdObjects{}
	err = spec.LoadAndAssign(objs, nil)
	if err != nil {
		return fmt.Errorf("load objs: %w", err)
	}
	b.closers = append(b.closers, objs)

	err = b.attachOneCg(ebpf.AttachCGroupInet4Connect, objs.LfwdConnect4)
	if err != nil {
		return err
	}

	// lfwd
	err = b.attachOneCg(ebpf.AttachCGroupUDP4Sendmsg, objs.LfwdSendmsg4)
	if err != nil {
		return err
	}

	err = b.attachOneCg(ebpf.AttachCgroupInet4GetPeername, objs.LfwdGetpeername4)
	if err != nil {
		return err
	}
	err = b.attachOneCg(ebpf.AttachCGroupInet6Connect, objs.LfwdConnect6)
	if err != nil {
		return err
	}
	err = b.attachOneCg(ebpf.AttachCGroupUDP6Sendmsg, objs.LfwdSendmsg6)
	if err != nil {
		return err
	}
	err = b.attachOneCg(ebpf.AttachCgroupInet6GetPeername, objs.LfwdGetpeername6)
	if err != nil {
		return err
	}

	b.lfwdBlockedPorts = objs.lfwdMaps.BlockedPorts
	// cfwd: attached to each docker container netns, but not the machine itself
	b.cfwdHostIps = objs.lfwdMaps.CfwdHostIps
	b.cfwdContainerMetas = objs.lfwdMaps.CfwdContainerMetas
	b.cfwdNetnsProg = objs.CfwdSkLookup
	return nil
}

func (b *ContainerBpfManager) attachCfwdNetns(prog *ebpf.Program, key string) error {
	nsFd, err := unix.Open("/run/docker/netns/"+key, unix.O_RDONLY|unix.O_CLOEXEC, 0)
	if err != nil {
		return fmt.Errorf("open netns: %w", err)
	}
	defer unix.Close(nsFd)

	l, err := link.AttachNetNs(nsFd, prog)
	if err != nil {
		return fmt.Errorf("attach: %w", err)
	}
	b.cfwdAttachedNsKeys[key] = l
	return nil
}

func checkIsNsfs(entry fs.DirEntry) bool {
	// check if it's a namespace. docker leaves non-bind-mounted files behind until GC
	fd, err := unix.Open("/run/docker/netns/"+entry.Name(), unix.O_RDONLY|unix.O_CLOEXEC, 0)
	if err != nil {
		return false
	}
	defer unix.Close(fd)

	_, err = unix.IoctlGetInt(fd, unix.NS_GET_NSTYPE)
	return err == nil
}

// NOTE: runs in container mount ns
// called with c.mu held for read
func (b *ContainerBpfManager) CfwdUpdateNetNamespaces(entries []fs.DirEntry) error {
	if b.cfwdNetnsProg == nil {
		return nil
	}

	var realKeys []string
	for _, entry := range entries {
		if checkIsNsfs(entry) {
			realKeys = append(realKeys, entry.Name())
		}
	}

	for _, nsKey := range realKeys {
		if _, ok := b.cfwdAttachedNsKeys[nsKey]; ok {
			// existing
			continue
		}

		// this is new
		logrus.WithField("netns", nsKey).Debug("attach cfwd netns")
		err := b.attachCfwdNetns(b.cfwdNetnsProg, nsKey)
		if err != nil {
			return err
		}
	}

	// detach old entries
	for k, v := range b.cfwdAttachedNsKeys {
		if !slices.Contains(realKeys, k) {
			logrus.WithField("netns", k).Debug("detach cfwd netns")
			err := v.Close()
			if err != nil {
				logrus.WithError(err).Error("close cfwd netns link")
			}
			delete(b.cfwdAttachedNsKeys, k)
		}
	}

	return nil
}

func ipToCfwdKey(ip net.IP) lfwdCfwdIpKey {
	key := lfwdCfwdIpKey{}
	// reinterpret and copy big endian
	// also map 4-in-6
	copy((*[16]byte)(unsafe.Pointer(&key.Ip6or4))[:], ip.To16())
	return key
}

// called with c.mu held for read
func (b *ContainerBpfManager) CfwdAddHostIP(ip net.IP) error {
	if b.cfwdHostIps == nil {
		return nil
	}

	return b.cfwdHostIps.Put(ipToCfwdKey(ip), byte(1))
}

// called with c.mu held for read
func (b *ContainerBpfManager) CfwdRemoveHostIP(ip net.IP) error {
	if b.cfwdHostIps == nil {
		return nil
	}

	return b.cfwdHostIps.Delete(ipToCfwdKey(ip))
}

// called with c.mu held for read
func (b *ContainerBpfManager) CfwdAddContainerMeta(ip net.IP, meta CfwdContainerMeta) error {
	if b.cfwdContainerMetas == nil {
		return nil
	}

	return b.cfwdContainerMetas.Put(ipToCfwdKey(ip), meta)
}

// called with c.mu held for read
func (b *ContainerBpfManager) CfwdRemoveContainerMeta(ip net.IP) error {
	if b.cfwdContainerMetas == nil {
		return nil
	}

	return b.cfwdContainerMetas.Delete(ipToCfwdKey(ip))
}

// called with c.mu held
func (b *ContainerBpfManager) AttachPmon(includeNft bool) (*ringbuf.Reader, error) {
	// k3s cgroup ID = inode
	var cgroupID uint64
	if includeNft {
		// create the k3s cgroup ahead of time. we want to watch kube-proxy
		err := os.MkdirAll(b.cgPath+"/"+ChildCgroupName+"/k3s", 0755)
		if err != nil {
			return nil, fmt.Errorf("create k3s cgroup: %w", err)
		}

		// get inode
		var stat unix.Stat_t
		err = unix.Stat(b.cgPath+"/"+ChildCgroupName+"/k3s", &stat)
		if err != nil {
			return nil, fmt.Errorf("stat k3s cgroup: %w", err)
		}
		logrus.WithField("cgroupID", stat.Ino).Debug("created k3s cgroup")
		cgroupID = stat.Ino
	}

	// must load a new instance to set a different netns cookie in config map
	// maps are per-program instance
	// and this is an unpinned program (no ref in /sys/fs/bpf), so it'll be destroyed
	// when we close fds
	spec, err := loadPmon()
	if err != nil {
		return nil, fmt.Errorf("load spec: %w", err)
	}

	// set netns cookie filter
	err = spec.RewriteConstants(map[string]any{
		"config_netns_cookie": b.netnsCookie,
		"config_cgroup_id":    cgroupID,
	})
	if err != nil {
		return nil, fmt.Errorf("configure: %w", err)
	}

	objs := pmonObjects{}
	err = spec.LoadAndAssign(&objs, nil)
	if err != nil {
		return nil, fmt.Errorf("load objs: %w", err)
	}
	b.closers = append(b.closers, &objs)

	err = b.attachOneCg(ebpf.AttachCGroupInet4PostBind, objs.PmonPostBind4)
	if err != nil {
		return nil, err
	}
	err = b.attachOneCg(ebpf.AttachCGroupInet4Connect, objs.PmonConnect4)
	if err != nil {
		return nil, err
	}
	err = b.attachOneCg(ebpf.AttachCGroupUDP4Recvmsg, objs.PmonRecvmsg4)
	if err != nil {
		return nil, err
	}
	err = b.attachOneCg(ebpf.AttachCGroupUDP4Sendmsg, objs.PmonSendmsg4)
	if err != nil {
		return nil, err
	}
	err = b.attachOneCg(ebpf.AttachCgroupInet4GetPeername, objs.PmonGetpeername4)
	if err != nil {
		return nil, err
	}

	err = b.attachOneCg(ebpf.AttachCGroupInet6PostBind, objs.PmonPostBind6)
	if err != nil {
		return nil, err
	}
	err = b.attachOneCg(ebpf.AttachCGroupInet6Connect, objs.PmonConnect6)
	if err != nil {
		return nil, err
	}
	err = b.attachOneCg(ebpf.AttachCGroupUDP6Recvmsg, objs.PmonRecvmsg6)
	if err != nil {
		return nil, err
	}
	err = b.attachOneCg(ebpf.AttachCGroupUDP6Sendmsg, objs.PmonSendmsg6)
	if err != nil {
		return nil, err
	}
	err = b.attachOneCg(ebpf.AttachCgroupInetSockRelease, objs.PmonSockRelease)
	if err != nil {
		return nil, err
	}
	err = b.attachOneCg(ebpf.AttachCgroupInet6GetPeername, objs.PmonGetpeername6)
	if err != nil {
		return nil, err
	}

	if includeNft {
		err = b.attachOneTracing(objs.NfTablesNewrule)
		if err != nil {
			return nil, err
		}
		err = b.attachOneTracing(objs.NfTablesDelrule)
		if err != nil {
			return nil, err
		}
	}

	reader, err := ringbuf.NewReader(objs.pmonMaps.NotifyRing)
	if err != nil {
		return nil, fmt.Errorf("create reader: %w", err)
	}
	b.closers = append(b.closers, reader)

	return reader, nil
}

func MonitorPmon(reader *ringbuf.Reader, fn func(PmonEvent) error) error {
	var rec ringbuf.Record
	for {
		// read one event
		err := reader.ReadInto(&rec)
		if err != nil {
			if errors.Is(err, os.ErrClosed) {
				return nil
			} else {
				return fmt.Errorf("read: %w", err)
			}
		}

		// notify event = u8
		ev := PmonEvent{
			DirtyFlags: LtypeFlags(rec.RawSample[0]),
		}

		// trigger callback
		err = fn(ev)
		if err != nil {
			logrus.WithError(err).Error("pmon callback failed")
		}
	}
}
