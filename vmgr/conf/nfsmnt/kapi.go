package nfsmnt

import (
	"bytes"
	"fmt"
	"regexp"
	"strconv"
	"sync"
	"time"
	"unsafe"

	nfs_sys_prot "github.com/buildbarn/go-xdr/pkg/protocols/darwin_nfs_sys_prot"
	"github.com/orbstack/macvirt/vmgr/util/pspawn"
	"github.com/orbstack/macvirt/vmgr/vmconfig"

	"golang.org/x/sys/unix"
)

var (
	initializeNFSOnce sync.Once

	macOSBuildVersionPattern = regexp.MustCompile("^([0-9]+)([A-Z]+)([0-9]+).*")
)

func toNfstime32(d time.Duration) *nfs_sys_prot.Nfstime32 {
	nanos := d.Nanoseconds()
	return &nfs_sys_prot.Nfstime32{
		Seconds:  int32(nanos / 1e9),
		Nseconds: uint32(nanos % 1e9),
	}
}

// getMacOSBuildVersion returns the build version of the currently
// running instance of macOS. For example, on macOS 13.0.1, it will
// return (22, 'A', 400).
func getMacOSBuildVersion() (int64, byte, int64, error) {
	osVersion, err := unix.Sysctl("kern.osversion")
	if err != nil {
		return 0, 0, 0, fmt.Errorf("sysctl: %w", err)
	}
	submatches := macOSBuildVersionPattern.FindStringSubmatch(osVersion)
	if submatches == nil {
		return 0, 0, 0, fmt.Errorf("invalid macOS version: %#v", osVersion)
	}
	major, err := strconv.ParseInt(submatches[1], 10, 64)
	if err != nil {
		return 0, 0, 0, fmt.Errorf("invalid macOS major version: %#v", submatches[1])
	}
	daily, err := strconv.ParseInt(submatches[3], 10, 64)
	if err != nil {
		return 0, 0, 0, fmt.Errorf("invalid macOS daily version: %#v", submatches[3])
	}
	return major, submatches[2][0], daily, nil
}

type Spec struct {
	IsUnix     bool
	Addr       string
	TcpPort    uint16
	TargetPath string
}

func SupportsUnixSocket() (bool, error) {
	osMajor, osMinor, osDaily, err := getMacOSBuildVersion()
	if err != nil {
		return false, err
	}
	return osMajor > 22 || (osMajor == 22 && (osMinor > 'E' || (osMinor == 'E' && osDaily >= 118))), nil
}

func doMount(spec Spec) error {
	// macOS may require us to perform certain initialisation steps
	// before attempting to create the NFS mount, such as loading
	// the kernel extension containing the NFS client.
	//
	// Instead of trying to mimic those steps, call mount_nfs(8) in
	// such a way that the arguments are valid, but is guaranteed to
	// fail quickly.
	initializeNFSOnce.Do(func() {
		pspawn.Command("/sbin/mount_nfs", "0.0.0.0:/", "/").Run()
	})

	// Construct attributes that are provided to mount(2). For NFS,
	// these attributes are stored in an XDR message. Similar to how
	// NFSv4's fattr4 works, the attributes need to be emitted in
	// increasing order by bitmask field.
	attrMask := make(nfs_sys_prot.Bitmap, nfs_sys_prot.NFS_MATTR_BITMAP_LEN)
	var attrVals bytes.Buffer

	// Don't bother setting up a callback service, as we don't issue
	// CB_NOTIFY operations. Using this option is also a requirement
	// for making NFSv4 over UNIX sockets work.
	// nocallback - fix nfs client id EINVAL with unix socket. callbacks are unused anyway - they're for delegation handoff with multiple clients
	// mutejukebox = don't show "fs not responding" dialog
	// can't use namedattr: Linux nfs server doesn't support it because it's more like resource streams than xattrs
	attrMask[0] |= 1 << nfs_sys_prot.NFS_MATTR_FLAGS
	flags := nfs_sys_prot.NfsMattrFlags{
		Mask: []uint32{
			1<<nfs_sys_prot.NFS_MFLAG_NOCALLBACK |
				1<<nfs_sys_prot.NFS_MFLAG_SOFT |
				1<<nfs_sys_prot.NFS_MFLAG_INTR |
				1<<nfs_sys_prot.NFS_MFLAG_NFC |
				1<<nfs_sys_prot.NFS_MFLAG_MUTEJUKEBOX,
		},
		Value: []uint32{
			1<<nfs_sys_prot.NFS_MFLAG_NOCALLBACK |
				1<<nfs_sys_prot.NFS_MFLAG_SOFT |
				1<<nfs_sys_prot.NFS_MFLAG_INTR |
				1<<nfs_sys_prot.NFS_MFLAG_NFC |
				1<<nfs_sys_prot.NFS_MFLAG_MUTEJUKEBOX,
		},
	}
	flags.WriteTo(&attrVals)

	// Explicitly request the use of NFSv4.0.
	attrMask[0] |= 1 << nfs_sys_prot.NFS_MATTR_NFS_VERSION
	nfs_sys_prot.WriteNfsMattrNfsVersion(&attrVals, 4)
	attrMask[0] |= 1 << nfs_sys_prot.NFS_MATTR_NFS_MINOR_VERSION
	nfs_sys_prot.WriteNfsMattrNfsMinorVersion(&attrVals, 0)

	// rwsize=131072,readahead=64 optimal for vsock
	attrMask[0] |= 1 << nfs_sys_prot.NFS_MATTR_READ_SIZE
	nfs_sys_prot.WriteNfsMattrRsize(&attrVals, 131072)

	attrMask[0] |= 1 << nfs_sys_prot.NFS_MATTR_WRITE_SIZE
	nfs_sys_prot.WriteNfsMattrWsize(&attrVals, 131072)

	attrMask[0] |= 1 << nfs_sys_prot.NFS_MATTR_READAHEAD
	nfs_sys_prot.WriteNfsMattrReadahead(&attrVals, 64)

	isUnix := spec.IsUnix
	if isUnix {
		// "ticotsord" is the X/Open Transport Interface (XTI)
		// equivalent of AF_LOCAL with SOCK_STREAM.
		attrMask[0] |= 1 << nfs_sys_prot.NFS_MATTR_SOCKET_TYPE
		nfs_sys_prot.WriteNfsMattrSocketType(&attrVals, "ticotsord")
	}

	if !isUnix {
		attrMask[0] |= 1 << nfs_sys_prot.NFS_MATTR_NFS_PORT
		nfs_sys_prot.WriteNfsMattrNfsPort(&attrVals, nfs_sys_prot.NfsMattrNfsPort(spec.TcpPort))
	}

	// disable dead timeout
	// combined with mmap'd holder fd to prevent is_mobile "squishy" force unmount, this allows using soft mounts (so truly stuck operations will return ETIMEDOUT instead of being stuck in uninterruptible wait) without risk of auto force unmount
	// check XNU code for how this works. by default "soft" enables deadtimeout=60, but nm_curdeadtimeout <= 0 disables it
	attrMask[0] |= 1 << nfs_sys_prot.NFS_MATTR_DEAD_TIMEOUT
	toNfstime32(0).WriteTo(&attrVals)

	attrMask[0] |= 1 << nfs_sys_prot.NFS_MATTR_FS_LOCATIONS
	fsLocations := nfs_sys_prot.NfsFsLocations{
		NfslLocation: []nfs_sys_prot.NfsFsLocation{{
			NfslServer: []nfs_sys_prot.NfsFsServer{{
				NfssName:    "OrbStack",
				NfssAddress: []string{spec.Addr},
			}},
		}},
	}
	fsLocations.WriteTo(&attrVals)

	// subpath doesn't matter with nfsv4 + hacked up mount
	// but on macOS 14.4+ Finder uses it for folder name, and :/ shows ':'
	attrMask[0] |= 1 << nfs_sys_prot.NFS_MATTR_MNTFROM
	nfs_sys_prot.WriteNfsMattrMntfrom(&attrVals, "OrbStack:/OrbStack")

	if isUnix {
		attrMask[0] |= 1 << nfs_sys_prot.NFS_MATTR_LOCAL_NFS_PORT
		nfs_sys_prot.WriteNfsMattrLocalNfsPort(&attrVals, spec.Addr)
	}

	// Construct the nfs_mount_args message and serialize it.
	for attrMask[len(attrMask)-1] == 0 {
		attrMask = attrMask[:len(attrMask)-1]
	}
	mountArgs := nfs_sys_prot.NfsMountArgs{
		ArgsVersion:    nfs_sys_prot.NFS_ARGSVERSION_XDR,
		XdrArgsVersion: nfs_sys_prot.NFS_XDRARGS_VERSION_0,
		NfsMountAttrs: nfs_sys_prot.NfsMattr{
			Attrmask: attrMask,
			AttrVals: attrVals.Bytes(),
		},
	}
	mountArgs.ArgsLength = uint32(mountArgs.GetEncodedSizeBytes())

	mountArgsBuf := bytes.NewBuffer(make([]byte, 0, mountArgs.ArgsLength))
	if _, err := mountArgs.WriteTo(mountArgsBuf); err != nil {
		return fmt.Errorf("marshal nfs mount args: %w", err)
	}

	// Call mount(2) with the serialized nfs_mount_args message.
	mountFlags := unix.MNT_NOATIME
	if vmconfig.Get().MountHideShared {
		mountFlags |= unix.MNT_DONTBROWSE
	}
	if err := unix.Mount("nfs", spec.TargetPath, mountFlags, unsafe.Pointer(&mountArgsBuf.Bytes()[0])); err != nil {
		return fmt.Errorf("mount(): %w", err)
	}

	return nil
}
