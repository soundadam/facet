//go:build !windows

package launch

import (
	"context"
	"os"
	"os/exec"
	"os/signal"
	"syscall"

	"github.com/soundadam/facet/internal/profile"
)

// detach puts the supervisor in its own session so it outlives the
// terminal that started it.
func detach(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
}

// terminate asks the browser to quit the way a window manager would.
func terminate(p *os.Process) error {
	return p.Signal(syscall.SIGTERM)
}

// NotifyStop returns a context that is cancelled when the supervisor is
// asked to stop: by Stop, the terminal going away or Ctrl-C.
func NotifyStop(ctx context.Context) (context.Context, context.CancelFunc) {
	return signal.NotifyContext(ctx, os.Interrupt, syscall.SIGTERM, syscall.SIGHUP)
}

// Stop asks a running profile's supervisor to quit the browser, or the
// browser itself when the supervisor is gone.
func Stop(state *profile.RunState) error {
	pid := state.SupervisorPID
	if state.Orphaned() {
		pid = state.BrowserPID
	}
	return syscall.Kill(pid, syscall.SIGTERM)
}
