package tests

import (
	"testing"
)

func TestScliCreateStopStartDelete(t *testing.T) {
	t.Parallel()

	// create
	_, err := runScli("orbctl", "create", "alpine", "otest2")
	checkT(t, err)

	// stop
	_, err = runScli("orbctl", "stop", "otest2")
	checkT(t, err)

	// start
	_, err = runScli("orbctl", "start", "otest2")
	checkT(t, err)

	// restart
	_, err = runScli("orbctl", "restart", "otest2")
	checkT(t, err)

	// run command
	out, err := runScli("orb", "-m", "otest2", "true")
	checkT(t, err)
	// make sure output is empty, no spinner
	if out != "" {
		t.Fatalf("expected empty output, got: %s", out)
	}
	out, err = runScli("orbctl", "run", "-m", "otest2", "true")
	checkT(t, err)
	// make sure output is empty, no spinner
	if out != "" {
		t.Fatalf("expected empty output, got: %s", out)
	}

	// delete
	_, err = runScli("orbctl", "delete", "otest2")
	checkT(t, err)
}

func TestScliCloudInit(t *testing.T) {
	t.Parallel()

	// create with cloud-init
	_, err := runScli("orbctl", "create", "ubuntu", "otest3", "--user-data", "cloud-init.yml")
	checkT(t, err)

	// check file
	out, err := runScli("orbctl", "run", "-m", "otest3", "cat", "/etc/cltest")
	checkT(t, err)
	if out != "it works!\n" {
		t.Fatalf("expected test, got: %s", out)
	}

	// delete
	_, err = runScli("orbctl", "delete", "otest3")
	checkT(t, err)
}
