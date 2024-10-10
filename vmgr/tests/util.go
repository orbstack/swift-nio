package tests

import "os/exec"

// this function exists because we always want to use debug even if prod links are in /usr/local/bin
func runScli(args ...string) (string, error) {
	// (tests run from vmgr/tests)
	cmd := exec.Command("../../out/scli")
	cmd.Args = args
	o, err := cmd.CombinedOutput()
	if err != nil {
		return "", err
	}
	return string(o), nil
}
