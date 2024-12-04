//go:build darwin

package util

import "github.com/orbstack/macvirt/vmgr/util/pspawn"

func RunDisclaimTCC(combinedArgs ...string) (string, error) {
	cmd := makeRunCmd(combinedArgs...)
	// disclaim TCC responsibility
	cmd.PspawnAttr = &pspawn.PspawnAttr{
		DisclaimTCCResponsibility: true,
	}

	return finishRun(cmd, combinedArgs)
}
