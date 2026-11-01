package launch

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"log"
	"os"
	"os/exec"
	"strings"
	"time"

	"github.com/soundadam/facet/internal/bridge"
	"github.com/soundadam/facet/internal/profile"
)

// Supervisor owns one running profile: the proxy bridge, when the proxy
// needs credentials, and the Chromium process. If the supervisor dies the
// bridge goes with it, and the browser's requests fail rather than leave
// without the proxy.
type Supervisor struct {
	Store   *profile.Store
	Profile *profile.Profile
	Kernel  *Kernel
	// Password is the upstream proxy password from the keychain.
	Password string
	URLs     []string
	Logger   *log.Logger
	// Output receives Chromium's stdout and stderr.
	Output io.Writer
}

// Run starts the browser and blocks until it exits or ctx is cancelled,
// in which case the browser is asked to quit.
func (s *Supervisor) Run(ctx context.Context) error {
	p, store := s.Profile, s.Store
	opt := Options{UserDataDir: store.UserDataDir(p.ID), CacheDir: store.CacheDir(p.ID), URLs: s.URLs}
	bridgeURL := ""
	switch {
	case p.Proxy.Direct():
	case p.Proxy.Authenticated():
		if s.Password == "" {
			s.Logger.Printf("warning: no password stored for proxy user %q; trying without one", p.Proxy.Username)
		}
		b, err := bridge.Start(p.Proxy.URL(s.Password), s.Logger)
		if err != nil {
			return fmt.Errorf("start proxy bridge: %w", err)
		}
		defer b.Close()
		bridgeURL = b.URL()
		opt.ProxyServer = bridgeURL
		s.Logger.Printf("bridge %s -> %s", bridgeURL, p.Proxy)
	default:
		opt.ProxyServer = DirectProxy(p.Proxy)
	}

	args, warnings := Args(p, s.Kernel, opt)
	for _, w := range warnings {
		s.Logger.Printf("warning: %s", w)
	}
	if err := os.MkdirAll(opt.UserDataDir, 0o700); err != nil {
		return err
	}
	cmd := exec.Command(s.Kernel.Exe, args...)
	cmd.Stdout, cmd.Stderr = s.Output, s.Output
	s.Logger.Printf("starting Chromium %s: %s", s.Kernel.Version, strings.Join(args, " "))
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("start browser: %w", err)
	}
	if err := store.WriteRunState(p.ID, profile.RunState{
		SupervisorPID: os.Getpid(),
		BrowserPID:    cmd.Process.Pid,
		Bridge:        bridgeURL,
		Started:       time.Now().UTC().Truncate(time.Second),
	}); err != nil {
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
		return err
	}
	defer store.ClearRunState(p.ID)

	exited := make(chan error, 1)
	go func() { exited <- cmd.Wait() }()
	select {
	case err := <-exited:
		s.Logger.Printf("browser exited: %v", exitText(err))
		return err
	case <-ctx.Done():
		s.Logger.Printf("stopping browser (pid %d)", cmd.Process.Pid)
		_ = terminate(cmd.Process)
		select {
		case <-exited:
		case <-time.After(10 * time.Second):
			_ = cmd.Process.Kill()
			<-exited
		}
		return nil
	}
}

func exitText(err error) string {
	if err == nil {
		return "status 0"
	}
	return err.Error()
}

// Detach starts `<self> run <id> [urls]` in its own session with output
// going to the profile's log, and waits until it reports the browser as
// started. If it exits first, the error carries the tail of the log.
func Detach(ctx context.Context, store *profile.Store, p *profile.Profile, urls []string) (*profile.RunState, error) {
	self, err := os.Executable()
	if err != nil {
		return nil, err
	}
	logPath := store.LogPath(p.ID)
	logFile, err := os.OpenFile(logPath, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o600)
	if err != nil {
		return nil, err
	}
	defer logFile.Close()
	devNull, err := os.Open(os.DevNull)
	if err != nil {
		return nil, err
	}
	defer devNull.Close()

	cmd := exec.Command(self, append([]string{"run", p.ID, "--"}, urls...)...)
	// The child must see the same store even when it came from a flag.
	cmd.Env = append(os.Environ(), "FACET_HOME="+store.Root)
	cmd.Stdin, cmd.Stdout, cmd.Stderr = devNull, logFile, logFile
	detach(cmd)
	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("start supervisor: %w", err)
	}
	exited := make(chan error, 1)
	go func() { exited <- cmd.Wait() }()

	deadline := time.After(30 * time.Second)
	tick := time.NewTicker(100 * time.Millisecond)
	defer tick.Stop()
	for {
		select {
		case err := <-exited:
			return nil, fmt.Errorf("supervisor exited before the browser started (%s)%s", exitText(err), logTail(logPath, 15))
		case <-deadline:
			return nil, fmt.Errorf("browser did not start within 30s; see %s", logPath)
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-tick.C:
			state, _ := store.RunState(p.ID)
			if state != nil && state.SupervisorPID == cmd.Process.Pid {
				return state, nil
			}
		}
	}
}

// NewWindow hands URLs to an already running profile. Chromium's process
// singleton forwards them to the live browser and this process exits.
func NewWindow(k *Kernel, userDataDir string, urls []string) error {
	cmd := exec.Command(k.Exe, append([]string{"--user-data-dir=" + userDataDir}, urls...)...)
	var out bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &out
	done := make(chan error, 1)
	if err := cmd.Start(); err != nil {
		return err
	}
	go func() { done <- cmd.Wait() }()
	select {
	case err := <-done:
		if err != nil {
			return fmt.Errorf("open window: %v: %s", err, strings.TrimSpace(out.String()))
		}
		return nil
	case <-time.After(15 * time.Second):
		// Not forwarded within a reasonable time; leave it to finish on its own.
		return errors.New("open window: the running browser did not answer within 15s")
	}
}

func logTail(path string, n int) string {
	f, err := os.Open(path)
	if err != nil {
		return ""
	}
	defer f.Close()
	var lines []string
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		lines = append(lines, sc.Text())
		if len(lines) > n {
			lines = lines[1:]
		}
	}
	if len(lines) == 0 {
		return ""
	}
	return ":\n  " + strings.Join(lines, "\n  ")
}
