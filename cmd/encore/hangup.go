package main

import (
	"os"
	"os/signal"
	"sync"
	"syscall"
)

// onHangup runs restore and then quit when the process gets SIGHUP (the tmux
// pane or terminal window closed, or `respawn-pane -k`). Bubble Tea handles
// SIGINT and SIGTERM by quitting, so deferred cleanup runs; SIGHUP would
// otherwise end the process at once and leave the tmux pane option set.
// restore runs first so the pane is put back even if quitting stalls on a
// dead terminal. stop undoes the handler.
func onHangup(restore, quit func()) (stop func()) {
	ch := make(chan os.Signal, 1)
	signal.Notify(ch, syscall.SIGHUP)
	done := make(chan struct{})
	go func() {
		select {
		case <-ch:
			restore()
			quit()
		case <-done:
		}
	}()
	return sync.OnceFunc(func() {
		signal.Stop(ch)
		close(done)
	})
}
