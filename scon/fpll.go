package main

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"sync"
)

type FpllManager struct {
	mu        sync.Mutex
	processes map[string]*os.Process
}

func NewFpllManager() *FpllManager {
	return &FpllManager{
		processes: make(map[string]*os.Process),
	}
}

func (f *FpllManager) StartMount(source, dest string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if _, ok := f.processes[dest]; ok {
		return fmt.Errorf("already mounted: %s", dest)
	}

	// pipe stdout to read mount status
	r, w, err := os.Pipe()
	if err != nil {
		return fmt.Errorf("create pipe: %w", err)
	}
	defer r.Close()

	cmd := exec.Command("/opt/orb/fpll", "-f", "-o", "clone_fd", "-o", "source="+source, dest)
	cmd.Stdout = w
	cmd.Stderr = os.Stderr
	err = cmd.Start()
	w.Close() // don't hang if process exits
	if err != nil {
		return fmt.Errorf("start fpll: %w", err)
	}

	// wait for mount to complete
	// read mount status
	buf := make([]byte, 1)
	_, err = r.Read(buf)
	if err != nil {
		return fmt.Errorf("read mount status: %w", err)
	}
	if buf[0] != '0' {
		return fmt.Errorf("mount failed: %s", dest)
	}

	f.processes[dest] = cmd.Process
	return nil
}

func (f *FpllManager) StopMount(dest string) error {
	f.mu.Lock()
	defer f.mu.Unlock()

	process, ok := f.processes[dest]
	if !ok {
		return fmt.Errorf("not mounted: %s", dest)
	}

	err := process.Kill()
	if err != nil {
		return fmt.Errorf("kill fpll: %w", err)
	}

	// must wait for fds to close
	_, err = process.Wait()
	if err != nil {
		return fmt.Errorf("wait fpll: %w", err)
	}

	delete(f.processes, dest)
	return nil
}

func (f *FpllManager) StopAll() error {
	f.mu.Lock()
	defer f.mu.Unlock()

	var errs []error
	for dest, process := range f.processes {
		err := process.Kill()
		if err != nil {
			errs = append(errs, fmt.Errorf("kill fpll: %w", err))
		}

		// must wait for fds to close
		_, err = process.Wait()
		if err != nil {
			errs = append(errs, fmt.Errorf("wait fpll: %w", err))
		}

		delete(f.processes, dest)
	}

	return errors.Join(errs...)
}
