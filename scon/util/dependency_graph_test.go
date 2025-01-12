package util

import (
	"fmt"
	"testing"
	"time"
)

func getSleepPrint(t *testing.T, s string) func() error {
	return func() error {
		t.Logf("start %s", s)
		time.Sleep(1 * time.Second)
		t.Logf("end %s", s)
		time.Sleep(1 * time.Second)
		return nil
	}
}

func TestDependencyGraph(t *testing.T) {
	runner := NewDependentTaskRunner[string](func(f func()) error {
		go f()
		return nil
	})

	runner.AddTask("-1", getSleepPrint(t, "-1"), nil)
	runner.AddTask("-2", getSleepPrint(t, "-2"), nil)
	runner.AddTask("-3", getSleepPrint(t, "-3"), nil)
	runner.AddTask("-4", getSleepPrint(t, "-4"), nil)
	runner.AddTask("(-1)-1", getSleepPrint(t, "(-1)-1"), []string{"-1"})
	runner.AddTask("(-1,-2,-3)-1", getSleepPrint(t, "(-1,-2,-3)-1"), []string{"-1", "-2", "-3"})
	runner.AddTask("(-4),((-1,-2,-3)-1)-1", getSleepPrint(t, "(-4),((-1,-2,-3)-1)-1"), []string{"-4", "(-1,-2,-3)-1"})
	runner.AddTask("((-1)-1),((-4),((-1,-2,-3)-1))-1", getSleepPrint(t, "((-1)-1),((-4),((-1,-2,-3)-1))-1"), []string{"(-1)-1", "(-4),((-1,-2,-3)-1)-1"})

	err := runner.Wait("((-1)-1),((-4),((-1,-2,-3)-1))-1")
	if err != nil {
		t.Fatal(err)
	}

	t.Log("complete")
}

func TestDependencyGraphCircular(t *testing.T) {
	runner := NewDependentTaskRunner[string](func(f func()) error {
		go f()
		return nil
	})

	runner.AddTask("a", getSleepPrint(t, "a"), []string{"d"})
	runner.AddTask("b", getSleepPrint(t, "b"), []string{"a"})
	runner.AddTask("c", getSleepPrint(t, "c"), []string{"b"})
	runner.AddTask("d", getSleepPrint(t, "d"), []string{"c"})

	err := runner.Wait("d")
	t.Log(err)
	if err == nil {
		t.Fatal("expected error")
	}
}

func TestDependencyGraphFailingDependency(t *testing.T) {
	runner := NewDependentTaskRunner[string](func(f func()) error {
		go f()
		return nil
	})

	runner.AddTask("a", func() error {
		return fmt.Errorf("a failed")
	}, nil)
	runner.AddTask("b", getSleepPrint(t, "b"), []string{"a"})
	runner.AddTask("c", getSleepPrint(t, "c"), []string{"b"})
	runner.AddTask("d", getSleepPrint(t, "d"), []string{"c"})

	err := runner.Wait("d")
	t.Log(err)
	if err == nil {
		t.Fatal(err)
	}
}

func TestDependencyGraphFailingSpawn(t *testing.T) {
	counter := 0
	runner := NewDependentTaskRunner[string](func(f func()) error {
		if counter == 1 {
			return fmt.Errorf("spawn failed")
		}
		counter++
		go f()
		return nil
	})

	runner.AddTask("a", getSleepPrint(t, "a"), nil)
	runner.AddTask("b", getSleepPrint(t, "b"), []string{"a"})
	runner.AddTask("c", getSleepPrint(t, "c"), []string{"b"})
	runner.AddTask("d", getSleepPrint(t, "d"), []string{"c"})

	err := runner.Wait("d")
	t.Log(err)
	if err == nil {
		t.Fatal("expected error")
	}
}
