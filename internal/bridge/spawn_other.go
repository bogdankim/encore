//go:build !darwin || !cgo

package bridge

import "os"

// spawnDisclaimed has no disclaim to offer here and starts path normally.
func spawnDisclaimed(path string, argv, env []string, files [3]*os.File) (*os.Process, bool, error) {
	p, err := os.StartProcess(path, argv, &os.ProcAttr{Env: env, Files: files[:]})
	return p, false, err
}

func responsiblePID(int) int { return -1 }
