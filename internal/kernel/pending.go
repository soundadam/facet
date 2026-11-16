package kernel

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"

	"github.com/soundadam/facet/internal/launch"
)

// A pending kernel is an archive downloaded and checked but not unpacked
// yet. The Homebrew cask leaves one, because its install steps run in a
// sandbox where hdiutil cannot attach a disk image; facet unpacks it the
// first time it looks for a kernel and finds none.

// PendingDir holds the pending archive and, beside it, its digest in
// <archive>.sha256.
func PendingDir() string {
	return filepath.Join(filepath.Dir(launch.ManagedDir()), "chromium-pending")
}

// Download fetches a and checks it like Install, then leaves it in
// PendingDir, replacing any archive already there.
func Download(ctx context.Context, a Asset, progress Progress) (string, error) {
	staging, err := newStaging()
	if err != nil {
		return "", err
	}
	defer os.RemoveAll(staging)
	name := filepath.Base(filepath.Clean("/" + a.Name))
	if err := download(ctx, a, filepath.Join(staging, name), progress); err != nil {
		return "", err
	}
	if err := os.WriteFile(filepath.Join(staging, name+".sha256"), []byte(a.Digest+"\n"), 0o644); err != nil {
		return "", err
	}
	dst := PendingDir()
	if err := os.RemoveAll(dst); err != nil {
		return "", err
	}
	keep := filepath.Join(staging, "pending")
	if err := os.Mkdir(keep, 0o755); err != nil {
		return "", err
	}
	for _, f := range []string{name, name + ".sha256"} {
		if err := os.Rename(filepath.Join(staging, f), filepath.Join(keep, f)); err != nil {
			return "", err
		}
	}
	if err := os.Rename(keep, dst); err != nil {
		return "", err
	}
	return filepath.Join(dst, name), nil
}

// Pending returns the pending archive and its digest ("" when upstream
// published none), or ok false when there is none.
func Pending() (path, digest string, ok bool) {
	entries, err := os.ReadDir(PendingDir())
	if err != nil {
		return "", "", false
	}
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || strings.HasSuffix(name, ".sha256") || strings.HasPrefix(name, ".") {
			continue
		}
		path = filepath.Join(PendingDir(), name)
		d, err := os.ReadFile(path + ".sha256")
		if err != nil {
			continue
		}
		return path, strings.TrimSpace(string(d)), true
	}
	return "", "", false
}

// InstallPending unpacks the pending archive into launch.ManagedDir and
// removes it. An archive that fails its digest is removed too; one that
// fails to unpack stays for the next try.
func InstallPending(ctx context.Context) (*launch.Kernel, error) {
	path, digest, ok := Pending()
	if !ok {
		return nil, os.ErrNotExist
	}
	k, err := InstallFile(ctx, path, digest)
	var bad digestError
	if err == nil || errors.As(err, &bad) {
		os.RemoveAll(PendingDir())
	}
	return k, err
}

// ClearPending drops a pending archive that a later install made stale.
func ClearPending() {
	os.RemoveAll(PendingDir())
}
