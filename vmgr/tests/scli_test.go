package tests

import (
	"testing"
)

func TestScliCreateStopStartDelete(t *testing.T) {
	t.Parallel()

	// create
	n := name("scli")
	_, err := runScli("orb", "create", "alpine", n)
	checkT(t, err)

	// stop
	_, err = runScli("orb", "stop", n)
	checkT(t, err)

	// start
	_, err = runScli("orb", "start", n)
	checkT(t, err)

	// restart
	_, err = runScli("orb", "restart", n)
	checkT(t, err)

	// run command
	out, err := runScli("orb", "-m", n, "true")
	checkT(t, err)
	// make sure output is empty, no spinner
	if out != "" {
		t.Fatalf("expected empty output, got: %s", out)
	}
	out, err = runScli("orb", "run", "-m", n, "true")
	checkT(t, err)
	// make sure output is empty, no spinner
	if out != "" {
		t.Fatalf("expected empty output, got: %s", out)
	}

	// delete
	_, err = runScli("orb", "delete", n)
	checkT(t, err)
}

func TestScliCloudInit(t *testing.T) {
	t.Parallel()

	// create with cloud-init
	n := name("cloud-init")
	_, err := runScli("orb", "create", "ubuntu", n, "--user-data", "cloud-init.yml")
	checkT(t, err)

	// check file
	out, err := runScli("orb", "run", "-m", n, "cat", "/etc/cltest")
	checkT(t, err)
	if out != "it works!\n" {
		t.Fatalf("expected test, got: %s", out)
	}

	// delete
	_, err = runScli("orb", "delete", n)
	checkT(t, err)
}
