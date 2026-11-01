package kernel

import (
	"archive/zip"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/soundadam/facet/internal/launch"
)

func asset(name string) Asset {
	return Asset{Name: name, URL: "https://example.invalid/" + name, Size: 1}
}

var testReleases = []Release{
	{Version: "150.0.7871.186", Major: 150, Assets: []Asset{
		asset("ungoogled-chromium-150.0.7871.186-1-x86_64_linux.tar.xz"),
		asset("ungoogled-chromium-150.0.7871.186-1-x86_64_linux.AppImage"),
		asset("ungoogled-chromium_150.0.7871.186-1.1_windows_x64.zip"),
		asset("ungoogled-chromium_150.0.7871.186-1.1_installer_x64.exe"),
	}},
	{Version: "148.0.7778.215", Major: 148, Assets: []Asset{
		asset("ungoogled-chromium_148.0.7778.215-1.1_macos.dmg"),
		asset("ungoogled-chromium-148.0.7778.215-1-x86_64_linux.tar.xz"),
	}},
	{Version: "139.0.7258.154", Major: 139, Assets: []Asset{
		asset("ungoogled-chromium_139.0.7258.154-1.1_macos.dmg"),
	}},
}

func TestPick(t *testing.T) {
	cases := []struct {
		want, goos, goarch string
		version, file      string
		err                string
	}{
		{"", "darwin", "arm64", "148.0.7778.215", "_macos.dmg", ""},
		{"", "linux", "amd64", "150.0.7871.186", "x86_64_linux.tar.xz", ""},
		{"", "windows", "amd64", "150.0.7871.186", "_windows_x64.zip", ""},
		{"148", "linux", "amd64", "148.0.7778.215", "x86_64_linux.tar.xz", ""},
		{"v148.0.7778.215", "darwin", "arm64", "148.0.7778.215", "_macos.dmg", ""},
		{"", "darwin", "amd64", "", "", "macOS (Intel)"},
		{"", "linux", "arm64", "", "", "Linux (ARM)"},
		{"150", "darwin", "arm64", "", "", "has no macOS (Apple silicon) build"},
		{"139", "darwin", "arm64", "", "", "older than 142"},
		{"151", "linux", "amd64", "", "", "no release 151"},
		{"15", "linux", "amd64", "", "", "no release 15"},
	}
	for _, c := range cases {
		r, a, err := Pick(testReleases, c.want, c.goos, c.goarch)
		if c.err != "" {
			if err == nil || !strings.Contains(err.Error(), c.err) {
				t.Errorf("Pick(%q, %s/%s) = %s, %v; want error with %q", c.want, c.goos, c.goarch, r.Version, err, c.err)
			}
			continue
		}
		if err != nil || r.Version != c.version || !strings.HasSuffix(a.Name, c.file) {
			t.Errorf("Pick(%q, %s/%s) = %s %s, %v; want %s *%s", c.want, c.goos, c.goarch, r.Version, a.Name, err, c.version, c.file)
		}
	}
	if _, _, err := Pick(testReleases, "", "darwin", "amd64"); !errors.Is(err, ErrNoBuild) {
		t.Errorf("no Intel build: %v, want ErrNoBuild", err)
	}
}

func TestReleases(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/repos/"+launch.UpstreamRepo+"/releases" {
			http.NotFound(w, r)
			return
		}
		fmt.Fprint(w, `[
			{"tag_name": "148.0.7778.215", "assets": [{"name": "a.dmg", "digest": "sha256:00", "size": 3}]},
			{"tag_name": "150.0.7871.186", "assets": []},
			{"tag_name": "151.0.1.1", "prerelease": true},
			{"tag_name": "152.0.1.1", "draft": true},
			{"tag_name": "nightly"},
			{"tag_name": "142.0.7444.175"}
		]`)
	}))
	defer srv.Close()
	defer func(old string) { API = old }(API)
	API = srv.URL

	rels, err := Releases(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, r := range rels {
		got = append(got, fmt.Sprintf("%s/%d", r.Version, r.Major))
	}
	if strings.Join(got, " ") != "150.0.7871.186/150 148.0.7778.215/148 142.0.7444.175/142" {
		t.Fatalf("releases %v", got)
	}
	if d, ok := Digest(rels, "a.dmg"); !ok || d != "sha256:00" {
		t.Fatalf("Digest = %q, %v", d, ok)
	}
}

// fakeArchive is a zip laid out like upstream's archives, holding a kernel
// Inspect accepts on this platform.
func fakeArchive(t *testing.T, version string) []byte {
	t.Helper()
	top := "ungoogled-chromium_" + version + "/"
	files := map[string]string{}
	switch runtime.GOOS {
	case "darwin":
		files["Chromium.app/Contents/Info.plist"] = `<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0"><dict><key>CFBundleShortVersionString</key><string>` + version + `</string></dict></plist>`
		files["Chromium.app/Contents/MacOS/Chromium"] = "#!/bin/sh\n"
	case "windows":
		files["chrome.exe"] = "MZ"
		files[version+".manifest"] = ""
	default:
		files["chrome"] = "#!/bin/sh\necho Chromium " + version + "\n"
	}
	var buf bytes.Buffer
	w := zip.NewWriter(&buf)
	for name, body := range files {
		h := &zip.FileHeader{Name: top + name, Method: zip.Deflate}
		h.SetMode(0o755)
		f, err := w.CreateHeader(h)
		if err != nil {
			t.Fatal(err)
		}
		f.Write([]byte(body))
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func digestOf(b []byte) string {
	sum := sha256.Sum256(b)
	return "sha256:" + hex.EncodeToString(sum[:])
}

func TestInstallFile(t *testing.T) {
	t.Setenv("FACET_HOME", t.TempDir())
	ctx := context.Background()
	dir := t.TempDir()
	write := func(version string) (string, []byte) {
		b := fakeArchive(t, version)
		path := filepath.Join(dir, "fp-"+version+".zip")
		if err := os.WriteFile(path, b, 0o644); err != nil {
			t.Fatal(err)
		}
		return path, b
	}

	path, b := write("148.0.7778.215")
	k, err := InstallFile(ctx, path, digestOf(b))
	if err != nil {
		t.Fatal(err)
	}
	if k.Version != "148.0.7778.215" || !strings.HasPrefix(k.Exe, launch.ManagedDir()) {
		t.Fatalf("installed %+v", k)
	}

	// A newer one replaces it, and Locate finds it first.
	path, _ = write("150.0.7871.186")
	if _, err := InstallFile(ctx, path, ""); err != nil {
		t.Fatal(err)
	}
	t.Setenv("FACET_CHROMIUM", "")
	if k, err := launch.Locate(ctx); err != nil || k.Version != "150.0.7871.186" {
		t.Fatalf("Locate after reinstall: %+v, %v", k, err)
	}
	if left, _ := filepath.Glob(filepath.Join(filepath.Dir(launch.ManagedDir()), ".chromium-new-*")); len(left) != 0 {
		t.Fatalf("staging left behind: %v", left)
	}

	// A damaged archive and one too old leave the installed kernel alone.
	if _, err := InstallFile(ctx, path, digestOf([]byte("other"))); err == nil || !strings.Contains(err.Error(), "SHA-256") {
		t.Fatalf("wrong digest: %v", err)
	}
	path, _ = write("139.0.7258.154")
	if _, err := InstallFile(ctx, path, ""); !errors.Is(err, launch.ErrNoKernel) {
		t.Fatalf("old kernel: %v", err)
	}
	if k, err := launch.Inspect(ctx, launch.ManagedExe(launch.ManagedDir())); err != nil || k.Version != "150.0.7871.186" {
		t.Fatalf("kernel after failed installs: %+v, %v", k, err)
	}
}

func TestInstallDownload(t *testing.T) {
	t.Setenv("FACET_HOME", t.TempDir())
	b := fakeArchive(t, "148.0.7778.215")
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write(b)
	}))
	defer srv.Close()
	a := Asset{Name: "fp_macos.zip", URL: srv.URL + "/fp.zip", Size: int64(len(b)), Digest: digestOf(b)}
	var last int64
	k, err := Install(context.Background(), a, func(done, total int64) { last = done })
	if err != nil {
		t.Fatal(err)
	}
	if k.Version != "148.0.7778.215" || last != int64(len(b)) {
		t.Fatalf("installed %+v, progress ended at %d of %d", k, last, len(b))
	}
	a.Digest = digestOf([]byte("tampered"))
	if _, err := Install(context.Background(), a, nil); err == nil || !strings.Contains(err.Error(), "SHA-256") {
		t.Fatalf("tampered download: %v", err)
	}
}

func TestUnzipRejectsEscapes(t *testing.T) {
	var buf bytes.Buffer
	w := zip.NewWriter(&buf)
	f, _ := w.Create("../evil")
	f.Write([]byte("x"))
	w.Close()
	path := filepath.Join(t.TempDir(), "bad.zip")
	os.WriteFile(path, buf.Bytes(), 0o644)
	dir := t.TempDir()
	if err := unpackZip(path, dir); err == nil || !strings.Contains(err.Error(), "outside") {
		t.Fatalf("unpackZip: %v", err)
	}
}
