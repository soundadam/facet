package launch

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/soundadam/facet/internal/profile"
)

func testProfile() *profile.Profile {
	return &profile.Profile{
		Schema: profile.SchemaVersion, ID: "abcdefgh", Serial: 3, Name: "shop 1",
		Created: time.Date(2026, 10, 25, 0, 0, 0, 0, time.UTC),
		Fingerprint: profile.Fingerprint{
			Seed: 12345, Platform: profile.PlatformMacOS, Brand: "Chrome",
			HardwareConcurrency: 10, DisableSpoofing: []string{"gpu"},
		},
	}
}

func TestArgs(t *testing.T) {
	k := &Kernel{Major: 148}
	p := testProfile()
	p.Locale = profile.Locale{Timezone: "Asia/Tokyo", Language: "ja-JP"}
	p.StartURLs = []string{"https://example.com/"}
	args, warnings := Args(p, k, Options{UserDataDir: "/u", ProxyServer: "http://127.0.0.1:5555"})
	want := []string{
		"--user-data-dir=/u",
		"--fingerprint=12345",
		"--fingerprint-platform=macos",
		"--fingerprint-brand=Chrome",
		"--fingerprint-hardware-concurrency=10",
		"--disable-spoofing=gpu",
		"--timezone=Asia/Tokyo",
		"--lang=ja-JP",
		"--accept-lang=ja-JP,ja",
		"--proxy-server=http://127.0.0.1:5555",
		"--disable-quic",
		"--disable-non-proxied-udp",
		"--window-name=[3] shop 1",
		"--no-first-run",
		"--no-default-browser-check",
		"https://example.com/",
	}
	if !slices.Equal(args, want) {
		t.Fatalf("args:\n  %s\nwant:\n  %s", strings.Join(args, "\n  "), strings.Join(want, "\n  "))
	}
	if len(warnings) != 0 {
		t.Fatalf("warnings %v", warnings)
	}
}

func TestArgsURLOverrideAndDirect(t *testing.T) {
	p := testProfile()
	p.StartURLs = []string{"https://a.example/"}
	args, _ := Args(p, &Kernel{Major: 148}, Options{UserDataDir: "/u", URLs: []string{"https://b.example/"}})
	if args[len(args)-1] != "https://b.example/" || slices.Contains(args, "https://a.example/") {
		t.Fatalf("explicit URLs did not replace start URLs: %v", args)
	}
	for _, a := range args {
		if strings.HasPrefix(a, "--proxy-server") || a == "--disable-quic" || strings.HasPrefix(a, "--timezone") {
			t.Fatalf("direct profile without locale got %s", a)
		}
	}
}

func TestArgsOldKernel(t *testing.T) {
	p := testProfile()
	p.Fingerprint.DisableSpoofing = []string{"gpu", "canvas"}
	args, warnings := Args(p, &Kernel{Major: 143}, Options{UserDataDir: "/u"})
	if !slices.Contains(args, "--disable-gpu-fingerprint") {
		t.Fatalf("143 should use --disable-gpu-fingerprint: %v", args)
	}
	for _, a := range args {
		if strings.HasPrefix(a, "--disable-spoofing") {
			t.Fatalf("143 does not know %s", a)
		}
	}
	if len(warnings) != 1 || !strings.Contains(warnings[0], "canvas") {
		t.Fatalf("warnings %v", warnings)
	}
}

func TestProxyWithoutTimezoneWarns(t *testing.T) {
	_, warnings := Args(testProfile(), &Kernel{Major: 148}, Options{UserDataDir: "/u", ProxyServer: "socks5://h:1"})
	if len(warnings) != 1 || !strings.Contains(warnings[0], "timezone") {
		t.Fatalf("warnings %v", warnings)
	}
}

func TestAcceptLanguage(t *testing.T) {
	for in, want := range map[string]string{"": "", "en-US": "en-US,en", "zh-CN": "zh-CN,zh", "fr": "fr"} {
		if got := AcceptLanguage(in); got != want {
			t.Errorf("AcceptLanguage(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestDirectProxy(t *testing.T) {
	got := DirectProxy(profile.Proxy{Scheme: "socks5h", Host: "203.0.113.1", Port: 1080})
	if got != "socks5://203.0.113.1:1080" {
		t.Fatalf("DirectProxy = %s", got)
	}
}

func TestResolveExe(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("bundle paths are macOS paths")
	}
	exe, bundle := resolveExe("/Applications/Chromium.app/")
	if exe != "/Applications/Chromium.app/Contents/MacOS/Chromium" || bundle != "/Applications/Chromium.app" {
		t.Fatalf("resolveExe = %s, %s", exe, bundle)
	}
	if exe, bundle := resolveExe("/opt/fc/chrome"); exe != "/opt/fc/chrome" || bundle != "" {
		t.Fatalf("resolveExe(plain) = %s, %s", exe, bundle)
	}
}

func TestSiblingVersion(t *testing.T) {
	dir := t.TempDir()
	for _, name := range []string{"chrome.exe", "142.0.7444.175", "150.0.7871.186.manifest", "150.0.7871.99.manifest", "notes.txt"} {
		if err := os.WriteFile(filepath.Join(dir, name), nil, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if got := siblingVersion(dir); got != "150.0.7871.186" {
		t.Fatalf("siblingVersion = %q", got)
	}
	if got := siblingVersion(t.TempDir()); got != "" {
		t.Fatalf("siblingVersion(empty) = %q", got)
	}
}

func TestLocateFromEnv(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("fake kernels are shell scripts")
	}
	dir := t.TempDir()
	fake := filepath.Join(dir, "chrome")
	script := "#!/bin/sh\necho 'Chromium 148.0.7778.215 '\n"
	if err := os.WriteFile(fake, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("FACET_CHROMIUM", fake)
	k, err := Locate(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if k.Exe != fake || k.Version != "148.0.7778.215" || k.Major != 148 {
		t.Fatalf("Locate = %+v", k)
	}

	old := filepath.Join(dir, "old")
	if err := os.WriteFile(old, []byte("#!/bin/sh\necho 'Chromium 139.0.7258.154'\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("FACET_CHROMIUM", old)
	if _, err := Locate(context.Background()); err == nil || !strings.Contains(err.Error(), "142") {
		t.Fatalf("old kernel accepted: %v", err)
	}

	t.Setenv("FACET_CHROMIUM", filepath.Join(dir, "missing"))
	if _, err := Locate(context.Background()); err == nil || !strings.Contains(err.Error(), "FACET_CHROMIUM") {
		t.Fatalf("missing kernel: %v", err)
	}
}
