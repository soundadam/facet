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

// Locate finds the kernel: $FACET_CHROMIUM (an .app bundle or an
// executable), else the platform's usual install locations.
func Locate(ctx context.Context) (*Kernel, error) {
	var candidates []string
	if v := os.Getenv("FACET_CHROMIUM"); v != "" {
		candidates = []string{v}
	} else {
		candidates = defaultCandidates()
	}
	var tried []string
	for _, c := range candidates {
		exe, bundle := resolveExe(c)
		if info, err := os.Stat(exe); err != nil || info.IsDir() {
			tried = append(tried, c)
			continue
		}
		version, err := readVersion(ctx, exe, bundle)
		if err != nil {
			return nil, fmt.Errorf("kernel %s: %w", c, err)
		}
		major, _ := strconv.Atoi(strings.SplitN(version, ".", 2)[0])
		k := &Kernel{Exe: exe, Version: version, Major: major}
		if major < MinMajor {
			return k, fmt.Errorf("kernel %s is Chromium %s; facet needs fingerprint-chromium %d or newer", c, version, MinMajor)
		}
		return k, nil
	}
	return nil, fmt.Errorf("fingerprint-chromium not found (looked in %s); install it from https://github.com/adryfish/fingerprint-chromium/releases or set FACET_CHROMIUM",
		strings.Join(tried, ", "))
}

func defaultCandidates() []string {
	home, _ := os.UserHomeDir()
	if runtime.GOOS == "darwin" {
		return []string{"/Applications/Chromium.app", filepath.Join(home, "Applications", "Chromium.app")}
	}
	var out []string
	for _, name := range []string{"fingerprint-chromium", "chromium", "chrome"} {
		if p, err := exec.LookPath(name); err == nil {
			out = append(out, p)
		}
	}
	return append(out, filepath.Join(home, ".local", "share", "facet", "chromium", "chrome"))
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
