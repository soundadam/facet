package launch

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strconv"
	"syscall"

	"golang.org/x/sys/windows"

	"github.com/soundadam/facet/internal/profile"
)

// Windows has no SIGTERM, so Stop reaches the supervisor through a named
// event it watches, and the browser is closed through its windows.

// detach starts the supervisor without a console and outside the
// terminal's Ctrl-C group, so it outlives the window that started it.
func detach(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{
		CreationFlags: windows.CREATE_NEW_PROCESS_GROUP | windows.DETACHED_PROCESS,
		HideWindow:    true,
	}
}

// terminate asks the browser to quit: taskkill without /F posts WM_CLOSE
// to its windows, which Chromium treats like the user closing them.
func terminate(p *os.Process) error {
	return taskkill(p.Pid)
}

func taskkill(pid int) error {
	exe := filepath.Join(os.Getenv("SystemRoot"), "System32", "taskkill.exe")
	cmd := exec.Command(exe, "/PID", strconv.Itoa(pid))
	// The supervisor has no console, so a console tool would open one.
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true, CreationFlags: windows.CREATE_NO_WINDOW}
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("taskkill: %v: %s", err, out)
	}
	return nil
}

func stopEventName(supervisorPID int) *uint16 {
	name, _ := windows.UTF16PtrFromString(fmt.Sprintf(`Local\facet-stop-%d`, supervisorPID))
	return name
}

// NotifyStop returns a context that is cancelled when the supervisor is
// asked to stop: by Stop, through the named event, or by Ctrl-C.
func NotifyStop(ctx context.Context) (context.Context, context.CancelFunc) {
	ctx, stopSignals := signal.NotifyContext(ctx, os.Interrupt)
	ctx, cancel := context.WithCancel(ctx)
	event, err := windows.CreateEvent(nil, 1, 0, stopEventName(os.Getpid()))
	if err != nil {
		// Stop falls back to closing the browser directly.
		return ctx, func() { cancel(); stopSignals() }
	}
	go func() {
		defer windows.CloseHandle(event)
		for ctx.Err() == nil {
			if ev, _ := windows.WaitForSingleObject(event, 250); ev == windows.WAIT_OBJECT_0 {
				cancel()
				return
			}
		}
	}()
	return ctx, func() { cancel(); stopSignals() }
}

// Stop asks a running profile's supervisor to quit the browser, or the
// browser itself when the supervisor is gone or not listening.
func Stop(state *profile.RunState) error {
	if !state.Orphaned() {
		event, err := windows.OpenEvent(windows.EVENT_MODIFY_STATE, false, stopEventName(state.SupervisorPID))
		if err == nil {
			defer windows.CloseHandle(event)
			return windows.SetEvent(event)
		}
	}
	if state.BrowserPID <= 0 {
		return errors.New("no browser process to stop")
	}
	return taskkill(state.BrowserPID)
}
