package profile

import (
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"
)

func newTestStore(t *testing.T) *Store {
	t.Helper()
	dir := t.TempDir()
	return &Store{Root: filepath.Join(dir, "home"), Trash: filepath.Join(dir, "trash")}
}

func mustNew(t *testing.T, name, platform string) *Profile {
	t.Helper()
	p, err := New(name, platform, time.Date(2026, 10, 25, 8, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatal(err)
	}
	return p
}

func TestCreateLoadRoundTrip(t *testing.T) {
	s := newTestStore(t)
	p := mustNew(t, "work-01", PlatformWindows)
	p.Locale = Locale{Timezone: "America/Los_Angeles", Language: "en-US", AcceptLanguage: "en-US,en"}
	p.Proxy = Proxy{Scheme: "socks5", Host: "203.0.113.7", Port: 1080, Username: "alice"}
	if err := s.Create(p); err != nil {
		t.Fatal(err)
	}
	if p.ID == "" || p.Serial != 1 {
		t.Fatalf("id %q serial %d", p.ID, p.Serial)
	}
	got, err := s.Load(p.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got.Fingerprint, p.Fingerprint) {
		t.Fatalf("fingerprint %+v, want %+v", got.Fingerprint, p.Fingerprint)
	}
	if got.Proxy != p.Proxy || got.Locale != p.Locale || !got.Created.Equal(p.Created) {
		t.Fatalf("loaded %+v, want %+v", got, p)
	}
	info, err := os.Stat(s.tomlPath(p.ID))
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("profile.toml mode %v", info.Mode().Perm())
	}
}

func TestSerialsAndResolve(t *testing.T) {
	s := newTestStore(t)
	a, b := mustNew(t, "Alpha", PlatformMacOS), mustNew(t, "beta", PlatformWindows)
	for _, p := range []*Profile{a, b} {
		if err := s.Create(p); err != nil {
			t.Fatal(err)
		}
	}
	if b.Serial != 2 {
		t.Fatalf("second serial %d", b.Serial)
	}
	if err := s.Create(mustNew(t, "ALPHA", PlatformLinux)); err == nil {
		t.Fatal("duplicate name (case-insensitive) accepted")
	}
	for ref, want := range map[string]string{"2": b.ID, "#1": a.ID, "alpha": a.ID, b.ID: b.ID} {
		got, err := s.Resolve(ref)
		if err != nil || got.ID != want {
			t.Fatalf("Resolve(%q) = %v, %v; want %s", ref, got, err, want)
		}
	}
	if _, err := s.Resolve("gamma"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("Resolve(gamma) err %v", err)
	}
	list, err := s.List()
	if err != nil || len(list) != 2 || list[0].ID != a.ID {
		t.Fatalf("List = %v, %v", list, err)
	}
}

func TestRemoveMovesToTrashAndFreesName(t *testing.T) {
	s := newTestStore(t)
	p := mustNew(t, "gone", PlatformWindows)
	if err := s.Create(p); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(s.UserDataDir(p.ID), "Default"), 0o700); err != nil {
		t.Fatal(err)
	}
	dest, err := s.Remove(p)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dest, "user-data", "Default")); err != nil {
		t.Fatalf("trash copy missing user data: %v", err)
	}
	if _, err := os.Stat(s.Dir(p.ID)); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("profile dir still present: %v", err)
	}
	if err := s.Create(mustNew(t, "gone", PlatformWindows)); err != nil {
		t.Fatalf("name not reusable after remove: %v", err)
	}
}

func TestRunStateDropsDeadProcesses(t *testing.T) {
	s := newTestStore(t)
	p := mustNew(t, "run", PlatformWindows)
	if err := s.Create(p); err != nil {
		t.Fatal(err)
	}
	if err := s.WriteRunState(p.ID, RunState{SupervisorPID: os.Getpid(), BrowserPID: os.Getpid()}); err != nil {
		t.Fatal(err)
	}
	if st, _ := s.RunState(p.ID); st == nil {
		t.Fatal("live run state not reported")
	}
	if _, err := s.Remove(p); err == nil {
		t.Fatal("removed a running profile")
	}
	// PID 1 belongs to launchd/init, never to us, but a pid that cannot exist
	// is a cleaner stand-in for an exited process.
	if err := s.WriteRunState(p.ID, RunState{SupervisorPID: 1 << 30, BrowserPID: 1 << 30}); err != nil {
		t.Fatal(err)
	}
	if st, _ := s.RunState(p.ID); st != nil {
		t.Fatalf("stale run state reported: %+v", st)
	}
	if _, err := os.Stat(s.runPath(p.ID)); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("stale run.json not removed")
	}
}

func TestNewDefaults(t *testing.T) {
	mac := mustNew(t, "m", PlatformMacOS)
	if len(mac.Fingerprint.DisableSpoofing) != 1 || mac.Fingerprint.DisableSpoofing[0] != "gpu" {
		t.Fatalf("macOS profile spoofs the GPU: %v", mac.Fingerprint.DisableSpoofing)
	}
	win := mustNew(t, "w", PlatformWindows)
	if len(win.Fingerprint.DisableSpoofing) != 0 {
		t.Fatalf("windows profile disables %v", win.Fingerprint.DisableSpoofing)
	}
	if mac.Fingerprint.Seed == win.Fingerprint.Seed {
		t.Fatal("two profiles drew the same seed")
	}
	if _, err := New("x", "beos", time.Now()); err == nil {
		t.Fatal("unknown platform accepted")
	}
}

func TestParseProxy(t *testing.T) {
	p, pw, err := ParseProxy("socks5://u%40x:p%3Aw@[2001:db8::1]:1080")
	if err != nil {
		t.Fatal(err)
	}
	if p != (Proxy{Scheme: "socks5", Host: "2001:db8::1", Port: 1080, Username: "u@x"}) || pw != "p:w" {
		t.Fatalf("got %+v %q", p, pw)
	}
	if got := p.String(); got != "socks5://u%40x@[2001:db8::1]:1080" {
		t.Fatalf("String() = %s", got)
	}
	if p, _, err := ParseProxy("direct"); err != nil || !p.Direct() {
		t.Fatalf("direct: %+v %v", p, err)
	}
	for _, bad := range []string{"ftp://h:1", "http://h", "http://h:0", "http://h:1/x"} {
		if _, _, err := ParseProxy(bad); err == nil {
			t.Errorf("ParseProxy(%q) accepted", bad)
		}
	}
}

func TestValidateName(t *testing.T) {
	for _, bad := range []string{"", " a", "12", "a/b", "a\tb"} {
		if ValidateName(bad) == nil {
			t.Errorf("ValidateName(%q) accepted", bad)
		}
	}
	if err := ValidateName("工作 01"); err != nil {
		t.Fatal(err)
	}
}
