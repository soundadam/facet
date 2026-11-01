// Package kernel installs fingerprint-chromium from its upstream GitHub
// releases into launch.ManagedDir. facet ships no browser: it fetches
// upstream's own build for this platform and checks it against the SHA-256
// GitHub publishes for it.
package kernel

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/soundadam/facet/internal/launch"
)

// Release is one upstream release; its tag is the Chromium version.
type Release struct {
	Version string
	Major   int
	Page    string
	Assets  []Asset
}

// Asset is a file of a release.
type Asset struct {
	Name string `json:"name"`
	URL  string `json:"browser_download_url"`
	Size int64  `json:"size"`
	// Digest is GitHub's "sha256:<hex>".
	Digest string `json:"digest"`
}

// API is the GitHub API root; tests point it at a local server.
var API = "https://api.github.com"

// client takes its proxy from the environment (https_proxy and so on) and
// has no overall timeout: a 180 MB download takes what it takes.
var client = &http.Client{Transport: &http.Transport{
	Proxy:                 http.ProxyFromEnvironment,
	DialContext:           (&net.Dialer{Timeout: 10 * time.Second}).DialContext,
	TLSHandshakeTimeout:   10 * time.Second,
	ResponseHeaderTimeout: 30 * time.Second,
}}

func get(ctx context.Context, url, accept string) (*http.Response, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", "facet")
	req.Header.Set("Accept", accept)
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != http.StatusOK {
		resp.Body.Close()
		return nil, fmt.Errorf("GET %s: %s", url, resp.Status)
	}
	return resp, nil
}

var tagPattern = regexp.MustCompile(`^v?(\d+)\.\d+\.\d+\.\d+$`)

// Releases lists upstream's published releases, newest version first.
// Drafts, prereleases and tags that are not a Chromium version are left out.
func Releases(ctx context.Context) ([]Release, error) {
	resp, err := get(ctx, API+"/repos/"+launch.UpstreamRepo+"/releases?per_page=30", "application/vnd.github+json")
	if err != nil {
		return nil, fmt.Errorf("listing fingerprint-chromium releases: %w", err)
	}
	defer resp.Body.Close()
	var api []struct {
		TagName    string  `json:"tag_name"`
		HTMLURL    string  `json:"html_url"`
		Draft      bool    `json:"draft"`
		Prerelease bool    `json:"prerelease"`
		Assets     []Asset `json:"assets"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&api); err != nil {
		return nil, fmt.Errorf("listing fingerprint-chromium releases: %w", err)
	}
	var out []Release
	for _, r := range api {
		m := tagPattern.FindStringSubmatch(r.TagName)
		if r.Draft || r.Prerelease || m == nil {
			continue
		}
		major, _ := strconv.Atoi(m[1])
		out = append(out, Release{Version: strings.TrimPrefix(r.TagName, "v"), Major: major, Page: r.HTMLURL, Assets: r.Assets})
	}
	slices.SortFunc(out, func(a, b Release) int {
		switch {
		case launch.VersionLess(a.Version, b.Version):
			return 1
		case launch.VersionLess(b.Version, a.Version):
			return -1
		}
		return 0
	})
	return out, nil
}

// assetPatterns are upstream's file names for each platform, e.g.
// ungoogled-chromium_148.0.7778.215-1.1_macos.dmg (an Apple silicon build),
// ungoogled-chromium-150.0.7871.186-1-x86_64_linux.tar.xz and
// ungoogled-chromium_150.0.7871.186-1.1_windows_x64.zip. Upstream builds
// nothing for Intel Macs or ARM Linux and Windows.
var assetPatterns = map[string]*regexp.Regexp{
	"darwin/arm64":  regexp.MustCompile(`_macos(_arm64)?\.dmg$`),
	"darwin/amd64":  regexp.MustCompile(`_macos_(x64|x86_64)\.dmg$`),
	"linux/amd64":   regexp.MustCompile(`x86_64_linux\.tar\.xz$`),
	"linux/arm64":   regexp.MustCompile(`(arm64|aarch64)_linux\.tar\.xz$`),
	"windows/amd64": regexp.MustCompile(`_windows_x64\.zip$`),
	"windows/arm64": regexp.MustCompile(`_windows_arm64\.zip$`),
}

// Build returns r's archive for goos/goarch.
func (r Release) Build(goos, goarch string) (Asset, bool) {
	pattern := assetPatterns[goos+"/"+goarch]
	if pattern == nil {
		return Asset{}, false
	}
	for _, a := range r.Assets {
		if pattern.MatchString(a.Name) {
			return a, true
		}
	}
	return Asset{}, false
}

// ErrNoBuild means upstream has no build for this platform that facet can
// drive.
var ErrNoBuild = errors.New("no fingerprint-chromium build for this platform")

// Pick chooses what to install: the newest release at or above
// launch.MinMajor with a build for goos/goarch, or, when want is set, the
// newest one whose version is want or starts with want + ".".
func Pick(releases []Release, want, goos, goarch string) (Release, Asset, error) {
	want = strings.TrimPrefix(strings.TrimSpace(want), "v")
	platform := PlatformName(goos, goarch)
	matched := false
	for _, r := range releases {
		if want != "" && r.Version != want && !strings.HasPrefix(r.Version, want+".") {
			continue
		}
		matched = true
		if r.Major < launch.MinMajor {
			if want != "" {
				return r, Asset{}, fmt.Errorf("fingerprint-chromium %s is older than %d, the oldest facet drives", r.Version, launch.MinMajor)
			}
			continue
		}
		if a, ok := r.Build(goos, goarch); ok {
			return r, a, nil
		}
		if want != "" && r.Version == want {
			break
		}
	}
	switch {
	case want != "" && !matched:
		return Release{}, Asset{}, fmt.Errorf("fingerprint-chromium has no release %s", want)
	case want != "":
		return Release{}, Asset{}, fmt.Errorf("%w: fingerprint-chromium %s has no %s build", ErrNoBuild, want, platform)
	}
	return Release{}, Asset{}, fmt.Errorf("%w: fingerprint-chromium %d or newer has no %s build; see https://github.com/%s/releases",
		ErrNoBuild, launch.MinMajor, platform, launch.UpstreamRepo)
}

// PlatformName is how upstream's download table names a platform.
func PlatformName(goos, goarch string) string {
	os := map[string]string{"darwin": "macOS", "linux": "Linux", "windows": "Windows"}[goos]
	if os == "" {
		os = goos
	}
	arch := map[string]string{"amd64": "x86_64", "arm64": "ARM"}[goarch]
	if goos == "darwin" {
		arch = map[string]string{"amd64": "Intel", "arm64": "Apple silicon"}[goarch]
	}
	if arch == "" {
		arch = goarch
	}
	return os + " (" + arch + ")"
}
