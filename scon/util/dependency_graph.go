package util

import (
	"fmt"

	"github.com/orbstack/macvirt/vmgr/syncx"
)

type runningStatus int

const (
	runningStatusPending runningStatus = iota
	runningStatusRunning
	runningStatusCompleted
)

type TaskError[I comparable] struct {
	Origin I
	Err    error
}

func (e *TaskError[I]) Error() string {
	return fmt.Sprintf("from task %v: %s", e.Origin, e.Err.Error())
}

func (e *TaskError[I]) Unwrap() error {
	return e.Err
}

// lock always required to read or write this struct
type dependentTask[I comparable] struct {
	runner            *DependentTaskRunner[I]
	id                I
	fn                func() error
	dependencies      []I
	waitingDependents []*dependentTask[I]
	status            runningStatus
	err               error
	waiter            chan struct{}
}

func (t *dependentTask[I]) completed(err error) {
	t.err = err
	t.status = runningStatusCompleted

	close(t.waiter)
}

func (t *dependentTask[I]) doneChan() <-chan struct{} {
	return t.waiter
}

type DependentTaskRunner[I comparable] struct {
	mu syncx.Mutex

	tasks map[I]*dependentTask[I]

	runFunc func(func()) error
}

func NewDependentTaskRunner[I comparable](runFunc func(func()) error) *DependentTaskRunner[I] {
	return &DependentTaskRunner[I]{
		tasks:   make(map[I]*dependentTask[I]),
		runFunc: runFunc,
	}
}

func (r *DependentTaskRunner[I]) AddTask(id I, task func() error, dependencies []I) {
	r.mu.Lock()
	defer r.mu.Unlock()

	r.tasks[id] = &dependentTask[I]{
		runner:       r,
		id:           id,
		fn:           task,
		dependencies: dependencies,
		status:       runningStatusPending,
		waiter:       make(chan struct{}),
	}
}

func (r *DependentTaskRunner[I]) scheduleLocked(task *dependentTask[I], dependentPath []*dependentTask[I]) (bool, error) {
	if task.status == runningStatusCompleted {
		return true, task.err
	}

	// add dependent so that it is woken up when we finish
	if len(dependentPath) > 0 {
		// only add last dependency in the dependent path, since the others need that to be satisfied first anyways
		task.waitingDependents = append(task.waitingDependents, dependentPath[len(dependentPath)-1])
	}

	// don't schedule two of the same task
	if task.status == runningStatusRunning {
		return false, nil
	}

	// check for circular dependencies
	for _, dep := range dependentPath {
		if dep.id == task.id {
			idPath := make([]I, len(dependentPath)+1)
			for i, dep := range dependentPath {
				idPath[i] = dep.id
			}
			idPath[len(idPath)-1] = task.id
			return false, fmt.Errorf("circular dependency chain: %v", idPath)
		}
	}

	dependentPath = append(dependentPath, task)

	dependenciesUnsatisfied := false
	for _, dep := range task.dependencies {
		depTask, ok := r.tasks[dep]
		if !ok {
			return false, fmt.Errorf("task not found: %v", dep)
		}

		satisfied, err := r.scheduleLocked(depTask, dependentPath)
		if err != nil {
			return false, err
		}

		if !satisfied {
			dependenciesUnsatisfied = true
		}
	}

	// dependencies are not satisfied, they'll retry scheduling us when they're done
	if dependenciesUnsatisfied {
		return false, nil
	}

	// we're good to run, let's go!
	task.status = runningStatusRunning

	err := r.runFunc(func() {
		err := task.fn()
		if err != nil {
			err = &TaskError[I]{
				Origin: task.id,
				Err:    err,
			}
		}
		r.done(task, err)
	})
	if err != nil {
		return false, err
	}

	return false, nil
}

func (r *DependentTaskRunner[I]) runTaskLocked(task *dependentTask[I]) {
	_, err := r.scheduleLocked(task, nil)
	if err != nil {
		r.doneLocked(task, err)
	}
}

func (r *DependentTaskRunner[I]) Run(id I) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	task, ok := r.tasks[id]
	if !ok {
		return fmt.Errorf("task not found: %v", id)
	}

	r.runTaskLocked(task)

	return nil
}

func (r *DependentTaskRunner[I]) doneLocked(task *dependentTask[I], err error) {
	if task.status == runningStatusCompleted {
		// return immediately in case cyclic dependency chain
		return
	}

	if err == nil {
		task.completed(nil)

		for _, dependent := range task.waitingDependents {
			// rescheduled tasks already have their waitingDependents set
			r.runTaskLocked(dependent)
		}
	} else {
		task.completed(err)

		for _, dependent := range task.waitingDependents {
			// bubble error
			r.doneLocked(dependent, err)
		}
	}
}

func (r *DependentTaskRunner[I]) done(task *dependentTask[I], err error) {
	r.mu.Lock()
	defer r.mu.Unlock()

	r.doneLocked(task, err)
}

func (r *DependentTaskRunner[I]) Wait(id I) error {
	// only hold lock to grab the task and start
	r.mu.Lock()

	task, ok := r.tasks[id]
	if !ok {
		r.mu.Unlock()
		return fmt.Errorf("task not found: %v", id)
	}
	r.runTaskLocked(task)

	r.mu.Unlock()

	// waiting occurs without lock held
	<-task.doneChan()

	// lock til end of function to get result of task
	r.mu.Lock()
	defer r.mu.Unlock()

	return task.err
}
