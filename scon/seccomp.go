package main

import (
	"fmt"
	"os"
)

type SeccompPolicyType int

const (
	SeccompPolicyNone SeccompPolicyType = iota
	SeccompPolicyIsolated
	// rosetta
	SeccompPolicyEmulated
	SeccompPolicyEmulatedIsolated
	_seccompPolicyMax
)

// we block BTRFS_IOC_QUOTA_CTL in kernel now
// must keep privileged machines free of seccomp, because it breaks CRIU
// https://github.com/orbstack/orbstack/issues/958
const seccompPolicyBase = `2
denylist
`

// block prctl(PR_SET_MDWE, *) = EINVAL, new in kernel 6.3
// systemd sets it if MemoryDenyWriteExecute=yes (otherwise it tries to use seccomp which fails b/c rosetta doesn't support)
// Rosetta needs RWX for JIT so this is a no-go
const seccompPolicyEmulated = `
prctl errno 22 [0,65,SCMP_CMP_EQ]
`

// open_by_handle_at allows escape via inode opening
const seccompPolicyIsolated = `
init_module errno 38
finit_module errno 38
delete_module errno 38

kexec_load errno 1
open_by_handle_at errno 1
`

func writeSeccompPolicies(tmpDir string) ([_seccompPolicyMax]string, error) {
	policies := map[SeccompPolicyType]string{
		// none
		SeccompPolicyIsolated:         seccompPolicyBase + seccompPolicyIsolated,
		SeccompPolicyEmulated:         seccompPolicyBase + seccompPolicyEmulated,
		SeccompPolicyEmulatedIsolated: seccompPolicyBase + seccompPolicyEmulated + seccompPolicyIsolated,
	}
	var paths [_seccompPolicyMax]string

	for i, content := range policies {
		path := fmt.Sprintf("%s/seccomp%d.policy", tmpDir, i)
		paths[i] = path

		err := os.WriteFile(path, []byte(content), 0644)
		if err != nil {
			return paths, err
		}
	}

	return paths, nil
}
