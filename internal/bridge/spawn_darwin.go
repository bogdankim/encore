//go:build darwin && cgo

package bridge

/*
#include <dlfcn.h>
#include <signal.h>
#include <spawn.h>
#include <stdlib.h>

// responsibility_spawnattrs_setdisclaim is private libquarantine API (macOS
// 10.14+). LLDB and terminal emulators use it so the programs they start ask
// TCC for permissions as themselves. Looked up at run time so a missing symbol
// just means no disclaim.
typedef int (*encore_disclaim_fn)(posix_spawnattr_t *attr, int disclaim);
typedef pid_t (*encore_responsible_fn)(pid_t pid);

// encore_spawn starts path with fds 0-2 taken from in, out and err, every other
// descriptor closed, an empty signal mask and SIGPIPE at its default. With
// disclaim it also disclaims responsibility; *disclaimed says whether it did.
static int encore_spawn(pid_t *pid, const char *path, char *const argv[], char *const envp[],
                        int in, int out, int err, int disclaim, int *disclaimed) {
	posix_spawnattr_t attr;
	posix_spawn_file_actions_t fa;
	int rc = posix_spawnattr_init(&attr);
	if (rc != 0) return rc;
	rc = posix_spawn_file_actions_init(&fa);
	if (rc != 0) {
		posix_spawnattr_destroy(&attr);
		return rc;
	}
	sigset_t none, pipe;
	sigemptyset(&none);
	sigemptyset(&pipe);
	sigaddset(&pipe, SIGPIPE);
	rc = posix_spawnattr_setflags(&attr, POSIX_SPAWN_CLOEXEC_DEFAULT | POSIX_SPAWN_SETSIGMASK | POSIX_SPAWN_SETSIGDEF);
	if (rc == 0) rc = posix_spawnattr_setsigmask(&attr, &none);
	if (rc == 0) rc = posix_spawnattr_setsigdefault(&attr, &pipe);
	if (rc == 0) rc = posix_spawn_file_actions_adddup2(&fa, in, 0);
	if (rc == 0) rc = posix_spawn_file_actions_adddup2(&fa, out, 1);
	if (rc == 0) rc = posix_spawn_file_actions_adddup2(&fa, err, 2);
	*disclaimed = 0;
	if (rc == 0 && disclaim) {
		encore_disclaim_fn f = (encore_disclaim_fn)dlsym(RTLD_DEFAULT, "responsibility_spawnattrs_setdisclaim");
		if (f != NULL && f(&attr, 1) == 0) *disclaimed = 1;
	}
	if (rc == 0) rc = posix_spawn(pid, path, &fa, &attr, argv, envp);
	posix_spawn_file_actions_destroy(&fa);
	posix_spawnattr_destroy(&attr);
	return rc;
}

// encore_responsible_pid is the pid TCC attributes pid's requests to, or -1.
static pid_t encore_responsible_pid(pid_t pid) {
	encore_responsible_fn f = (encore_responsible_fn)dlsym(RTLD_DEFAULT, "responsibility_get_pid_responsible_for_pid");
	return f != NULL ? f(pid) : -1;
}
*/
import "C"

import (
	"fmt"
	"os"
	"syscall"
	"unsafe"
)

// spawnDisclaimed starts path with posix_spawn and responsibility disclaimed.
// files are the child's stdin, stdout and stderr.
func spawnDisclaimed(path string, argv, env []string, files [3]*os.File) (*os.Process, bool, error) {
	cpath := C.CString(path)
	defer C.free(unsafe.Pointer(cpath))
	cargv := cStrings(argv)
	defer freeCStrings(cargv)
	cenv := cStrings(env)
	defer freeCStrings(cenv)

	var pid C.pid_t
	var disclaimed C.int
	rc := C.encore_spawn(&pid, cpath, &cargv[0], &cenv[0],
		C.int(files[0].Fd()), C.int(files[1].Fd()), C.int(files[2].Fd()), 1, &disclaimed)
	if rc != 0 {
		return nil, false, &os.PathError{Op: "posix_spawn", Path: path, Err: syscall.Errno(rc)}
	}
	proc, err := os.FindProcess(int(pid))
	if err != nil {
		return nil, false, fmt.Errorf("bridge: find spawned process %d: %w", pid, err)
	}
	return proc, disclaimed != 0, nil
}

// responsiblePID is the pid macOS attributes pid's TCC requests to, or -1
// when the system doesn't say.
func responsiblePID(pid int) int {
	return int(C.encore_responsible_pid(C.pid_t(pid)))
}

// cStrings is a NULL-terminated C array of copies of ss.
func cStrings(ss []string) []*C.char {
	out := make([]*C.char, len(ss)+1)
	for i, s := range ss {
		out[i] = C.CString(s)
	}
	return out
}

func freeCStrings(cs []*C.char) {
	for _, c := range cs {
		if c != nil {
			C.free(unsafe.Pointer(c))
		}
	}
}
