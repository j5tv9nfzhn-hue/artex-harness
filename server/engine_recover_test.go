package server

import (
	"context"
	"testing"
	"time"
)

// A panic inside one task's goroutine used to kill the process: a resumed legacy
// task hit a pgx panic inside a read snapshot and the server exited with no HTTP
// surface left to say why (the 04:03 outage). The guard lives in runTaskRoutine so
// every task path is covered; this asserts the goroutine survives and still
// releases its WaitGroup slot, i.e. the task is not leaked as "running forever".
func TestRunTaskRoutineRecoversPanic(t *testing.T) {
	ctx, cancel := context.WithCancelCause(context.Background())
	rt := &taskRuntime{ctx: ctx, cancelCause: cancel}
	rt.wg.Add(1)

	done := make(chan struct{})
	runTaskRoutine(rt, func(context.Context) {
		defer close(done)
		panic("boom from a worker")
	})

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("task goroutine never ran")
	}

	// wg.Done runs before the recover, so waiting here must return rather than hang.
	waited := make(chan struct{})
	go func() { rt.wg.Wait(); close(waited) }()
	select {
	case <-waited:
	case <-time.After(2 * time.Second):
		t.Fatal("WaitGroup slot leaked after panic")
	}
}
