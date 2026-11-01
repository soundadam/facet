package kernel

import (
	"archive/zip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"hash"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/soundadam/facet/internal/launch"
)

// Progress is told how many bytes of a download have arrived, out of total
// (0 when the size is unknown).
type Progress func(done, total int64)

// Install downloads a, checks it against a.Digest when GitHub published
// one, and installs it into launch.ManagedDir, replacing the kernel there.
// The old kernel stays in place until the new one has been unpacked and
// its version read.
func Install(ctx context.Context, a Asset, progress Progress) (*launch.Kernel, error) {
	staging, err := newStaging()
	if err != nil {
		return nil, err
	}
	defer os.RemoveAll(staging)
	archive := filepath.Join(staging, filepath.Base(filepath.Clean("/"+a.Name)))
	if err := download(ctx, a, archive, progress); err != nil {
		return nil, err
	}
	return install(ctx, staging, archive)
}

// InstallFile installs a fingerprint-chromium archive that is already on
// disk: a .dmg on macOS, a .tar.xz on Linux, a .zip on Windows. A non-empty
// digest ("sha256:<hex>") is checked first.
func InstallFile(ctx context.Context, path, digest string) (*launch.Kernel, error) {
	if digest != "" {
		f, err := os.Open(path)
		if err != nil {
			return nil, err
		}
		h := sha256.New()
		_, err = io.Copy(h, f)
		f.Close()
		if err != nil {
			return nil, err
		}
		if err := checkDigest(h, digest, filepath.Base(path)); err != nil {
			return nil, err
		}
	}
	staging, err := newStaging()
	if err != nil {
		return nil, err
	}
	defer os.RemoveAll(staging)
	return install(ctx, staging, path)
}

// Digest returns the published digest of the release asset named name, so a
// file downloaded by hand can still be checked.
func Digest(releases []Release, name string) (string, bool) {
	for _, r := range releases {
		for _, a := range r.Assets {
			if a.Name == name && a.Digest != "" {
				return a.Digest, true
			}
		}
	}
	return "", false
}

// newStaging makes a scratch directory beside ManagedDir, on the same
// filesystem, so the finished kernel moves into place with a rename.
// Staging left behind by an install that was killed is cleared first.
func newStaging() (string, error) {
	parent := filepath.Dir(launch.ManagedDir())
	if err := os.MkdirAll(parent, 0o755); err != nil {
		return "", err
	}
	if old, _ := filepath.Glob(filepath.Join(parent, ".chromium-new-*")); len(old) > 0 {
		for _, d := range old {
			if info, err := os.Stat(d); err == nil && time.Since(info.ModTime()) > time.Hour {
				os.RemoveAll(d)
			}
		}
	}
	return os.MkdirTemp(parent, ".chromium-new-")
}

func download(ctx context.Context, a Asset, dst string, progress Progress) error {
	resp, err := get(ctx, a.URL, "application/octet-stream")
	if err != nil {
		return fmt.Errorf("downloading %s: %w", a.Name, err)
	}
	defer resp.Body.Close()
	f, err := os.Create(dst)
	if err != nil {
		return err
	}
	total := resp.ContentLength
	if total <= 0 {
		total = a.Size
	}
	h := sha256.New()
	_, err = io.Copy(io.MultiWriter(f, h), &counter{r: resp.Body, total: total, progress: progress})
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		return fmt.Errorf("downloading %s: %w", a.Name, err)
	}
	if a.Digest == "" {
		return nil
	}
	return checkDigest(h, a.Digest, a.Name)
}

func checkDigest(h hash.Hash, digest, name string) error {
	want, ok := strings.CutPrefix(digest, "sha256:")
	if !ok {
		return fmt.Errorf("%s: unsupported digest %q", name, digest)
	}
	if got := hex.EncodeToString(h.Sum(nil)); !strings.EqualFold(got, want) {
		return fmt.Errorf("%s: SHA-256 is %s, but upstream published %s; the file is damaged or not upstream's", name, got, want)
	}
	return nil
}

type counter struct {
	r           io.Reader
	done, total int64
	progress    Progress
}

func (c *counter) Read(p []byte) (int, error) {
	n, err := c.r.Read(p)
	c.done += int64(n)
	if c.progress != nil && n > 0 {
		c.progress(c.done, c.total)
	}
	return n, err
}

func install(ctx context.Context, staging, archive string) (*launch.Kernel, error) {
	dir := filepath.Join(staging, "unpacked")
	if err := os.Mkdir(dir, 0o755); err != nil {
		return nil, err
	}
	name := strings.ToLower(archive)
	var err error
	switch {
	case strings.HasSuffix(name, ".dmg"):
		err = unpackDMG(ctx, archive, dir, staging)
	case strings.HasSuffix(name, ".tar.xz"):
		err = unpackTarXZ(ctx, archive, dir)
	case strings.HasSuffix(name, ".zip"):
		err = unpackZip(archive, dir)
	default:
		return nil, fmt.Errorf("%s: expected upstream's .dmg (macOS), .tar.xz (Linux) or .zip (Windows)", filepath.Base(archive))
	}
	if err != nil {
		return nil, fmt.Errorf("unpacking %s: %w", filepath.Base(archive), err)
	}
	root, err := findRoot(dir)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", filepath.Base(archive), err)
	}
	if _, err := launch.Inspect(ctx, launch.ManagedExe(root)); err != nil {
		return nil, err
	}
	target := launch.ManagedDir()
	if err := swap(root, target, filepath.Join(staging, "old")); err != nil {
		return nil, err
	}
	return launch.Inspect(ctx, launch.ManagedExe(target))
}

// findRoot returns the directory holding the kernel in the layout
// ManagedExe expects: dir itself or one level down, as upstream's archives
// put everything in a single top-level folder.
func findRoot(dir string) (string, error) {
	want := filepath.Base(launch.ManagedExe(dir))
	if _, err := os.Stat(filepath.Join(dir, want)); err == nil {
		return dir, nil
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return "", err
	}
	for _, e := range entries {
		if e.IsDir() {
			if _, err := os.Stat(filepath.Join(dir, e.Name(), want)); err == nil {
				return filepath.Join(dir, e.Name()), nil
			}
		}
	}
	return "", fmt.Errorf("no %s inside; is this the %s build?", want, PlatformName(runtime.GOOS, runtime.GOARCH))
}

// swap moves root to target, keeping the kernel already at target in old
// until the move has worked.
func swap(root, target, old string) error {
	moved := false
	if _, err := os.Lstat(target); err == nil {
		if err := os.Rename(target, old); err != nil {
			return fmt.Errorf("moving the old kernel aside (is it still running?): %w", err)
		}
		moved = true
	}
	if err := os.Rename(root, target); err != nil {
		if moved {
			os.Rename(old, target)
		}
		return fmt.Errorf("installing the kernel: %w", err)
	}
	return nil
}

// unpackDMG copies the app out of a disk image. ditto leaves no quarantine
// attribute on the copy, so macOS opens it without App Translocation.
func unpackDMG(ctx context.Context, archive, dir, staging string) error {
	if runtime.GOOS != "darwin" {
		return errors.New("a .dmg installs only on macOS")
	}
	mnt := filepath.Join(staging, "mnt")
	if err := os.Mkdir(mnt, 0o755); err != nil {
		return err
	}
	cmd := exec.CommandContext(ctx, "hdiutil", "attach", "-nobrowse", "-readonly", "-noautoopen", "-mountpoint", mnt, archive)
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("hdiutil attach: %w: %s", err, strings.TrimSpace(string(out)))
	}
	defer func() {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		exec.CommandContext(ctx, "hdiutil", "detach", mnt, "-force").Run()
	}()
	entries, err := os.ReadDir(mnt)
	if err != nil {
		return err
	}
	for _, e := range entries {
		if e.Name() == "Chromium.app" {
			out, err := exec.CommandContext(ctx, "ditto", filepath.Join(mnt, e.Name()), filepath.Join(dir, e.Name())).CombinedOutput()
			if err != nil {
				return fmt.Errorf("ditto: %w: %s", err, strings.TrimSpace(string(out)))
			}
			return nil
		}
	}
	return errors.New("no Chromium.app in the disk image")
}

func unpackTarXZ(ctx context.Context, archive, dir string) error {
	out, err := exec.CommandContext(ctx, "tar", "-xJf", archive, "-C", dir).CombinedOutput()
	if err != nil {
		return fmt.Errorf("tar: %w: %s (tar needs xz; install the xz-utils or xz package)", err, strings.TrimSpace(string(out)))
	}
	return nil
}

func unpackZip(archive, dir string) error {
	r, err := zip.OpenReader(archive)
	if err != nil {
		return err
	}
	defer r.Close()
	for _, f := range r.File {
		path := filepath.Join(dir, filepath.FromSlash(f.Name))
		if !inside(dir, path) {
			return fmt.Errorf("%s points outside the archive", f.Name)
		}
		mode := f.Mode()
		switch {
		case mode.IsDir():
			err = os.MkdirAll(path, 0o755)
		case mode&os.ModeSymlink != 0:
			err = unzipLink(f, dir, path)
		default:
			err = unzipFile(f, path, mode.Perm()|0o600)
		}
		if err != nil {
			return err
		}
	}
	return nil
}

func unzipFile(f *zip.File, path string, perm os.FileMode) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	src, err := f.Open()
	if err != nil {
		return err
	}
	defer src.Close()
	dst, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, perm)
	if err != nil {
		return err
	}
	_, err = io.Copy(dst, src)
	if cerr := dst.Close(); err == nil {
		err = cerr
	}
	return err
}

func unzipLink(f *zip.File, dir, path string) error {
	src, err := f.Open()
	if err != nil {
		return err
	}
	target, err := io.ReadAll(io.LimitReader(src, 4096))
	src.Close()
	if err != nil {
		return err
	}
	link := string(target)
	if filepath.IsAbs(link) || !inside(dir, filepath.Join(filepath.Dir(path), link)) {
		return fmt.Errorf("%s links outside the archive", f.Name)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	return os.Symlink(link, path)
}

func inside(dir, path string) bool {
	rel, err := filepath.Rel(dir, path)
	return err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) && !filepath.IsAbs(rel)
}
