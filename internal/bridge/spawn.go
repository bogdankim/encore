package bridge

import (
	"fmt"
	"io"
	"os"
	"os/exec"
	"time"
)

// child is a running bridge process and the parent's ends of its pipes.
type child struct {
	proc       *os.Process
	stdin      *os.File
	stdout     *os.File
	stderrDone chan struct{} // closed when stderr hits EOF
	disclaimed bool          // TCC treats the child as its own responsible process
}

// startChild runs path with stdin and stdout on pipes and stderr copied to
// stderr (nil discards it). With disclaim, macOS attributes the child's
// privacy (TCC) requests to the child itself instead of to the terminal that
// started encore, so the prompts and System Settings entries name encore.
func startChild(path string, args, env []string, stderr io.Writer, disclaim bool) (*child, error) {
	if env == nil {
		env = os.Environ()
	}
	argv := append([]string{path}, args...)

	var opened []*os.File
	pipe := func() (r, w *os.File, err error) {
		if r, w, err = os.Pipe(); err == nil {
			opened = append(opened, r, w)
		}
		return
	}
	fail := func(err error) (*child, error) {
		for _, f := range opened {
			f.Close()
		}
		return nil, err
	}
	inR, inW, err := pipe()
	if err != nil {
		return fail(err)
	}
	outR, outW, err := pipe()
	if err != nil {
		return fail(err)
	}
	errR, errW, err := pipe()
	if err != nil {
		return fail(err)
	}

	files := [3]*os.File{inR, outW, errW}
	c := &child{stdin: inW, stdout: outR, stderrDone: make(chan struct{})}
	if disclaim {
		c.proc, c.disclaimed, err = spawnDisclaimed(path, argv, env, files)
	} else {
		c.proc, err = os.StartProcess(path, argv, &os.ProcAttr{Env: env, Files: files[:]})
	}
	if err != nil {
		return fail(err)
	}
	// The child has its own copies now; closing ours lets EOF through.
	inR.Close()
	outW.Close()
	errW.Close()

	if stderr == nil {
		stderr = io.Discard
	}
	go func() {
		io.Copy(stderr, errR)
		errR.Close()
		close(c.stderrDone)
	}()
	return c, nil
}

// wait reaps the process, gives its stderr up to a second to drain (a
// grandchild may still hold it open) and closes stdin and stdout, so a
// crash-restart loop doesn't leak descriptors. Its error matches
// exec.Cmd.Wait's.
func (c *child) wait() error {
	state, err := c.proc.Wait()
	select {
	case <-c.stderrDone:
	case <-time.After(time.Second):
	}
	c.stdin.Close()
	c.stdout.Close()
	if err != nil {
		return fmt.Errorf("bridge: wait: %w", err)
	}
	if !state.Success() {
		return &exec.ExitError{ProcessState: state}
	}
	return nil
}
