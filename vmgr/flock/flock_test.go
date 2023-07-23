package flock

import (
	"os"
	"testing"
)

func TestLock(t *testing.T) {
	t.Parallel()

	file, err := os.CreateTemp("", "flock_test")
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	defer os.Remove(file.Name())

	err = Lock(file)
	if err != nil {
		t.Fatal(err)
	}
}

func TestLockWait(t *testing.T) {
	t.Parallel()

	file, err := os.CreateTemp("", "flock_test")
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	defer os.Remove(file.Name())

	err = Lock(file)
	if err != nil {
		t.Fatal(err)
	}

	err = WaitLock(file)
	if err != nil {
		t.Fatal(err)
	}
}

func TestUnlock(t *testing.T) {
	t.Parallel()

	file, err := os.CreateTemp("", "flock_test")
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	defer os.Remove(file.Name())

	err = Lock(file)
	if err != nil {
		t.Fatal(err)
	}

	err = Unlock(file)
	if err != nil {
		t.Fatal(err)
	}
}

func TestReadPidNone(t *testing.T) {
	t.Parallel()

	file, err := os.CreateTemp("", "flock_test")
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	defer os.Remove(file.Name())

	pid, err := ReadPid(file.Name())
	if err != nil {
		t.Fatal(err)
	}

	if pid != 0 {
		t.Fatal("expected pid to be 0")
	}
}
