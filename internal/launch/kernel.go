// Package launch turns a profile into a running fingerprint-chromium: it
// finds the kernel, maps profile fields to the flags that kernel version
// understands, and supervises the browser together with the proxy bridge.
package launch

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"time"
)

// MinMajor is the oldest kernel facet drives: 142 is the first build whose
// GPU spoofing can be switched off, which macOS profiles depend on.
const MinMajor = 142

// Kernel is an installed fingerprint-chromium build.
type Kernel struct {
	// Exe is the browser executable (inside the bundle on macOS).
	Exe string
	// Version is the full Chromium version, e.g. "148.0.7778.215".
	Version string
	Major   int
}

// ErrNoKernel marks the errors Locate returns when there is no usable
// kernel: none found, or only one older than MinMajor.
var ErrNoKernel = errors.New("no usable fingerprint-chromium")

// Locate finds the kernel: $FACET_CHROMIUM (an .app bundle or an
// executable), else the one facet kernel install put in ManagedDir, else
// the platform's usual install locations.
func Locate(ctx context.Context) (*Kernel, error) {
	var candidates []string
	if v := os.Getenv("FACET_CHROMIUM"); v != "" {
		candidates = []string{v}
	} else {
		candidates = defaultCandidates()
	}
	var tried []string
	for _, c := range candidates {
		k, err := Inspect(ctx, c)
		if errors.Is(err, os.ErrNotExist) {
			tried = append(tried, c)
			continue
		}
		return k, err
	}
	hint := "run `facet kernel install` to download it from https://github.com/" + UpstreamRepo + "/releases, or set FACET_CHROMIUM"
	if os.Getenv("FACET_CHROMIUM") != "" {
		hint = "fix or unset FACET_CHROMIUM"
	}
	return nil, fmt.Errorf("%w: fingerprint-chromium not found (looked in %s); %s", ErrNoKernel, strings.Join(tried, ", "), hint)
}

// UpstreamRepo is fingerprint-chromium's GitHub repository.
const UpstreamRepo = "adryfish/fingerprint-chromium"

// Inspect reads the kernel at path, an .app bundle or an executable. It
// returns an error wrapping os.ErrNotExist when nothing is there, and the
// kernel together with an ErrNoKernel error when it is too old.
func Inspect(ctx context.Context, path string) (*Kernel, error) {
	exe, bundle := resolveExe(path)
	if info, err := os.Stat(exe); err != nil || info.IsDir() {
		return nil, fmt.Errorf("kernel %s: %w", path, os.ErrNotExist)
	}
	version, err := readVersion(ctx, exe, bundle)
	if err != nil {
		return nil, fmt.Errorf("kernel %s: %w", path, err)
	}
	major, _ := strconv.Atoi(strings.SplitN(version, ".", 2)[0])
	k := &Kernel{Exe: exe, Version: version, Major: major}
	if major < MinMajor {
		return k, fmt.Errorf("%w: kernel %s is Chromium %s; facet needs fingerprint-chromium %d or newer (facet kernel install gets one)", ErrNoKernel, path, version, MinMajor)
	}
	return k, nil
}

// ManagedDir is where facet kernel install keeps the kernel: chromium in
// $FACET_HOME when that is set, else in the per-user application data
// (~/Library/Application Support/facet on macOS, $XDG_DATA_HOME/facet or
// ~/.local/share/facet on Linux, %LOCALAPPDATA%\facet on Windows).
func ManagedDir() string {
	if v := os.Getenv("FACET_HOME"); v != "" {
		return filepath.Join(v, "chromium")
	}
	home, _ := os.UserHomeDir()
	switch runtime.GOOS {
	case "darwin":
		return filepath.Join(home, "Library", "Application Support", "facet", "chromium")
	case "windows":
		if d := os.Getenv("LOCALAPPDATA"); d != "" {
			return filepath.Join(d, "facet", "chromium")
		}
		return filepath.Join(home, "AppData", "Local", "facet", "chromium")
	}
	if d := os.Getenv("XDG_DATA_HOME"); d != "" {
		return filepath.Join(d, "facet", "chromium")
	}
	return filepath.Join(home, ".local", "share", "facet", "chromium")
}

// ManagedExe is the kernel path inside dir, laid out as facet kernel
// install leaves it.
func ManagedExe(dir string) string {
	switch runtime.GOOS {
	case "darwin":
		return filepath.Join(dir, "Chromium.app")
	case "windows":
		return filepath.Join(dir, "chrome.exe")
	}
	return filepath.Join(dir, "chrome")
}

func defaultCandidates() []string {
	out := []string{ManagedExe(ManagedDir())}
	home, _ := os.UserHomeDir()
	if runtime.GOOS == "windows" {
		return append(out, windowsCandidates()...)
	}
	if runtime.GOOS == "darwin" {
		return append(out, "/Applications/Chromium.app", filepath.Join(home, "Applications", "Chromium.app"))
	}
	for _, name := range []string{"fingerprint-chromium", "chromium", "chrome"} {
		if p, err := exec.LookPath(name); err == nil {
			out = append(out, p)
		}
	}
	return out
}

// windowsCandidates covers the installer, which puts Chromium under
// Application for one or all users.
func windowsCandidates() []string {
	var out []string
	for _, name := range []string{"fingerprint-chromium.exe", "chrome.exe"} {
		if p, err := exec.LookPath(name); err == nil {
			out = append(out, p)
		}
	}
	if d := os.Getenv("LOCALAPPDATA"); d != "" {
		out = append(out, filepath.Join(d, "Chromium", "Application", "chrome.exe"))
	}
	if d := os.Getenv("ProgramFiles"); d != "" {
		out = append(out, filepath.Join(d, "Chromium", "Application", "chrome.exe"))
	}
	return out
}

// resolveExe maps an .app bundle to its executable.
func resolveExe(path string) (exe, bundle string) {
	if strings.HasSuffix(strings.TrimSuffix(path, "/"), ".app") {
		bundle = strings.TrimSuffix(path, "/")
		name := strings.TrimSuffix(filepath.Base(bundle), ".app")
		return filepath.Join(bundle, "Contents", "MacOS", name), bundle
	}
	return path, ""
}

var versionPattern = regexp.MustCompile(`\b(\d+\.\d+\.\d+\.\d+)\b`)

func readVersion(ctx context.Context, exe, bundle string) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	var out []byte
	var err error
	if bundle != "" {
		// Reading Info.plist avoids starting the browser.
		out, err = exec.CommandContext(ctx, "plutil", "-extract", "CFBundleShortVersionString", "raw",
			filepath.Join(bundle, "Contents", "Info.plist")).Output()
	} else if runtime.GOOS == "windows" {
		// chrome.exe --version prints nothing on Windows; the build ships a
		// <version>.manifest file or <version> folder beside it instead.
		if v := siblingVersion(filepath.Dir(exe)); v != "" {
			return v, nil
		}
		return "", fmt.Errorf("read version: no <version>.manifest or <version> folder beside %s", exe)
	} else {
		out, err = exec.CommandContext(ctx, exe, "--version").Output()
	}
	if err != nil {
		return "", fmt.Errorf("read version: %w", err)
	}
	m := versionPattern.FindString(string(out))
	if m == "" {
		return "", errors.New("read version: no version in " + strings.TrimSpace(string(out)))
	}
	return m, nil
}

var siblingPattern = regexp.MustCompile(`^(\d+\.\d+\.\d+\.\d+)(\.manifest)?$`)

// siblingVersion returns the newest version named by a file or folder in
// dir, or "" when there is none.
func siblingVersion(dir string) string {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return ""
	}
	best := ""
	for _, e := range entries {
		m := siblingPattern.FindStringSubmatch(e.Name())
		if m != nil && (best == "" || VersionLess(best, m[1])) {
			best = m[1]
		}
	}
	return best
}

// VersionLess compares dotted versions numerically; a missing part counts
// as 0.
func VersionLess(a, b string) bool {
	pa, pb := strings.Split(a, "."), strings.Split(b, ".")
	for i := range max(len(pa), len(pb)) {
		var x, y int
		if i < len(pa) {
			x, _ = strconv.Atoi(pa[i])
		}
		if i < len(pb) {
			y, _ = strconv.Atoi(pb[i])
		}
		if x != y {
			return x < y
		}
	}
	return false
}
