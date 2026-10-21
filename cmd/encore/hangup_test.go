package main

import (
	"slices"
	"sync"
	"syscall"
	"testing"
	"time"
)

func TestHangupRestoresThenQuits(t *testing.T) {
	var (
		mu    sync.Mutex
		calls []string
	)
	record := func(s string) func() {
		return func() {
			mu.Lock()
			calls = append(calls, s)
			mu.Unlock()
		}
	}
	quit := make(chan struct{})
	stop := onHangup(record("restore"), func() { record("quit")(); close(quit) })
	defer stop()

	if err := syscall.Kill(syscall.Getpid(), syscall.SIGHUP); err != nil {
		t.Fatal(err)
	}
	select {
	case <-quit:
	case <-time.After(2 * time.Second):
		t.Fatal("no quit after SIGHUP")
	}
	mu.Lock()
	defer mu.Unlock()
	if !slices.Equal(calls, []string{"restore", "quit"}) {
		t.Fatalf("calls = %v, want restore then quit", calls)
	}
}
