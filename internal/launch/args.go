package launch

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/soundadam/facet/internal/profile"
)

// Options are the per-launch inputs that are not part of the profile.
type Options struct {
	UserDataDir string
	// CacheDir keeps the HTTP and code caches beside the profile; on macOS
	// Chromium would otherwise put them under ~/Library/Caches.
	CacheDir string
	// ProxyServer is the value for --proxy-server: the bridge, or the
	// upstream itself when it needs no credentials. Empty means direct.
	ProxyServer string
	// URLs override the profile's start URLs when non-empty.
	URLs []string
}

// Args builds the Chromium command line for p. Warnings describe profile
// settings this kernel cannot honour; the launch still proceeds.
func Args(p *profile.Profile, k *Kernel, opt Options) (args, warnings []string) {
	fp := p.Fingerprint
	args = []string{
		"--user-data-dir=" + opt.UserDataDir,
		"--fingerprint=" + strconv.FormatUint(uint64(fp.Seed), 10),
		"--fingerprint-platform=" + fp.Platform,
	}
	add := func(flag, value string) {
		if value != "" {
			args = append(args, flag+"="+value)
		}
	}
	add("--disk-cache-dir", opt.CacheDir)
	add("--fingerprint-platform-version", fp.PlatformVersion)
	add("--fingerprint-brand", fp.Brand)
	add("--fingerprint-brand-version", fp.BrandVersion)
	if fp.HardwareConcurrency > 0 {
		add("--fingerprint-hardware-concurrency", strconv.Itoa(fp.HardwareConcurrency))
	}

	if len(fp.DisableSpoofing) > 0 {
		if k.Major >= 144 {
			add("--disable-spoofing", strings.Join(fp.DisableSpoofing, ","))
		} else {
			// 142–143 can only switch GPU spoofing off, under its old name.
			for _, s := range fp.DisableSpoofing {
				if s == "gpu" {
					args = append(args, "--disable-gpu-fingerprint")
				} else {
					warnings = append(warnings, fmt.Sprintf("Chromium %d cannot disable %s spoofing (needs 144+); it stays spoofed", k.Major, s))
				}
			}
		}
	}

	loc := p.Locale
	add("--timezone", loc.Timezone)
	add("--lang", loc.Language)
	accept := loc.AcceptLanguage
	if accept == "" {
		accept = AcceptLanguage(loc.Language)
	}
	add("--accept-lang", accept)

	if opt.ProxyServer != "" {
		args = append(args,
			"--proxy-server="+opt.ProxyServer,
			// QUIC and WebRTC UDP would leave beside the proxy.
			"--disable-quic",
			"--disable-non-proxied-udp",
		)
		if loc.Timezone == "" {
			warnings = append(warnings, "timezone is unset, so the browser reports this machine's zone while browsing through the proxy; set one with `facet set --timezone`")
		}
	}

	args = append(args,
		fmt.Sprintf("--window-name=[%d] %s", p.Serial, p.Name),
		"--no-first-run",
		"--no-default-browser-check",
	)
	urls := opt.URLs
	if len(urls) == 0 {
		urls = p.StartURLs
	}
	return append(args, urls...), warnings
}

// AcceptLanguage derives an Accept-Language list from a UI language:
// "en-US" gives "en-US,en", "zh-CN" gives "zh-CN,zh". A bare tag stays as is.
func AcceptLanguage(lang string) string {
	if lang == "" {
		return ""
	}
	base, _, found := strings.Cut(lang, "-")
	if !found {
		return lang
	}
	return lang + "," + base
}

// DirectProxy renders an upstream without credentials for --proxy-server.
// Chromium resolves names remotely for every SOCKS5 proxy, so socks5h is
// just socks5 to it.
func DirectProxy(p profile.Proxy) string {
	u := p.URL("")
	if u.Scheme == "socks5h" {
		u.Scheme = "socks5"
	}
	return u.String()
}
