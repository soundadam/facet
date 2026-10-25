// Package profile is facet's on-disk model: one directory per browser
// identity, holding profile.toml and the Chromium user-data directory.
package profile

import (
	"crypto/rand"
	"errors"
	"fmt"
	"math/big"
	"net"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"time"
)

// SchemaVersion is written to every profile.toml; Load refuses newer ones.
const SchemaVersion = 1

// Platforms the fingerprint kernel can claim (--fingerprint-platform).
const (
	PlatformMacOS   = "macos"
	PlatformWindows = "windows"
	PlatformLinux   = "linux"
)

// Spoofing surfaces --disable-spoofing accepts (Chrome 144+).
var spoofingSurfaces = []string{"font", "audio", "canvas", "clientrects", "gpu"}

// Proxy schemes facet accepts. Chromium cannot authenticate to a proxy on
// its own, so a proxy with a username goes through the local bridge.
var proxySchemes = []string{"http", "https", "socks5", "socks5h"}

type Profile struct {
	Schema      int         `toml:"schema"`
	ID          string      `toml:"id"`
	Serial      int         `toml:"serial"`
	Name        string      `toml:"name"`
	Notes       string      `toml:"notes,omitempty"`
	Created     time.Time   `toml:"created"`
	Fingerprint Fingerprint `toml:"fingerprint"`
	Locale      Locale      `toml:"locale"`
	Proxy       Proxy       `toml:"proxy"`
	StartURLs   []string    `toml:"start_urls,omitempty"`
}

type Fingerprint struct {
	// Seed is fixed at creation: the kernel derives canvas, audio, fonts and
	// the rest from it, so changing it makes this a different browser.
	Seed                uint32   `toml:"seed"`
	Platform            string   `toml:"platform"`
	PlatformVersion     string   `toml:"platform_version,omitempty"`
	Brand               string   `toml:"brand,omitempty"`
	BrandVersion        string   `toml:"brand_version,omitempty"`
	HardwareConcurrency int      `toml:"hardware_concurrency"`
	DisableSpoofing     []string `toml:"disable_spoofing,omitempty"`
}

type Locale struct {
	Timezone       string `toml:"timezone,omitempty"`
	Language       string `toml:"language,omitempty"`
	AcceptLanguage string `toml:"accept_language,omitempty"`
}

// Proxy is the upstream proxy without its password, which lives in the
// system keychain under the profile ID. The zero value means direct.
type Proxy struct {
	Scheme   string `toml:"scheme,omitempty"`
	Host     string `toml:"host,omitempty"`
	Port     int    `toml:"port,omitempty"`
	Username string `toml:"username,omitempty"`
}

func (p Proxy) Direct() bool { return p.Scheme == "" }

// Authenticated reports whether the proxy needs the bridge.
func (p Proxy) Authenticated() bool { return p.Username != "" }

// URL renders the proxy with the given password; pass "" for display.
func (p Proxy) URL(password string) *url.URL {
	if p.Direct() {
		return nil
	}
	u := &url.URL{Scheme: p.Scheme, Host: net.JoinHostPort(p.Host, strconv.Itoa(p.Port))}
	if p.Username != "" {
		if password != "" {
			u.User = url.UserPassword(p.Username, password)
		} else {
			u.User = url.User(p.Username)
		}
	}
	return u
}

func (p Proxy) String() string {
	if p.Direct() {
		return "direct"
	}
	return p.URL("").String()
}

// ParseProxy splits a proxy URL into the stored part and its password.
// "" and "direct" clear the proxy.
func ParseProxy(raw string) (Proxy, string, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" || raw == "direct" {
		return Proxy{}, "", nil
	}
	u, err := url.Parse(raw)
	if err != nil {
		return Proxy{}, "", fmt.Errorf("proxy %q: %w", raw, err)
	}
	scheme := strings.ToLower(u.Scheme)
	if !slices.Contains(proxySchemes, scheme) {
		return Proxy{}, "", fmt.Errorf("proxy %q: scheme must be one of %s", raw, strings.Join(proxySchemes, ", "))
	}
	host, portText := u.Hostname(), u.Port()
	if host == "" || portText == "" {
		return Proxy{}, "", fmt.Errorf("proxy %q: need host:port", raw)
	}
	port, err := strconv.Atoi(portText)
	if err != nil || port < 1 || port > 65535 {
		return Proxy{}, "", fmt.Errorf("proxy %q: bad port", raw)
	}
	if u.Path != "" && u.Path != "/" {
		return Proxy{}, "", fmt.Errorf("proxy %q: unexpected path", raw)
	}
	p := Proxy{Scheme: scheme, Host: host, Port: port}
	password := ""
	if u.User != nil {
		p.Username = u.User.Username()
		password, _ = u.User.Password()
	}
	return p, password, nil
}

// Validate checks the fields a launch depends on.
func (p *Profile) Validate() error {
	if p.Schema > SchemaVersion {
		return fmt.Errorf("profile %s: schema %d is newer than this facet understands (%d)", p.ID, p.Schema, SchemaVersion)
	}
	if err := ValidateName(p.Name); err != nil {
		return err
	}
	if p.Fingerprint.Seed == 0 {
		return errors.New("fingerprint seed must be non-zero")
	}
	switch p.Fingerprint.Platform {
	case PlatformMacOS, PlatformWindows, PlatformLinux:
	default:
		return fmt.Errorf("platform %q: must be macos, windows or linux", p.Fingerprint.Platform)
	}
	if p.Fingerprint.HardwareConcurrency < 0 || p.Fingerprint.HardwareConcurrency > 128 {
		return fmt.Errorf("hardware concurrency %d out of range", p.Fingerprint.HardwareConcurrency)
	}
	for _, s := range p.Fingerprint.DisableSpoofing {
		if !slices.Contains(spoofingSurfaces, s) {
			return fmt.Errorf("disable_spoofing %q: must be one of %s", s, strings.Join(spoofingSurfaces, ", "))
		}
	}
	if !p.Proxy.Direct() {
		if _, _, err := ParseProxy(p.Proxy.String()); err != nil {
			return err
		}
	}
	return nil
}

// ValidateName keeps names usable as a command-line argument.
func ValidateName(name string) error {
	if strings.TrimSpace(name) != name || name == "" {
		return fmt.Errorf("name %q: must be non-empty without surrounding spaces", name)
	}
	if len(name) > 60 {
		return fmt.Errorf("name %q: at most 60 bytes", name)
	}
	if _, err := strconv.Atoi(name); err == nil {
		return fmt.Errorf("name %q: a bare number would be read as a serial", name)
	}
	for _, r := range name {
		if r < 0x20 || r == 0x7f || r == '/' {
			return fmt.Errorf("name %q: no control characters or '/'", name)
		}
	}
	return nil
}

// Plausible core counts per claimed platform: the kernel's own pick from the
// seed can be 30 or 32, which no Mac reports.
var coreChoices = map[string][]int{
	PlatformMacOS:   {8, 10, 12, 14},
	PlatformWindows: {4, 8, 12, 16},
	PlatformLinux:   {4, 8, 16},
}

// New returns a profile with a fresh seed and defaults for the platform.
// Callers set ID and Serial through Store.Create.
func New(name, platform string, now time.Time) (*Profile, error) {
	seed, err := randomSeed()
	if err != nil {
		return nil, err
	}
	choices, ok := coreChoices[platform]
	if !ok {
		return nil, fmt.Errorf("platform %q: must be macos, windows or linux", platform)
	}
	pick, err := rand.Int(rand.Reader, big.NewInt(int64(len(choices))))
	if err != nil {
		return nil, err
	}
	p := &Profile{
		Schema:  SchemaVersion,
		Name:    name,
		Created: now.UTC().Truncate(time.Second),
		Fingerprint: Fingerprint{
			Seed:                seed,
			Platform:            platform,
			Brand:               "Chrome",
			HardwareConcurrency: choices[pick.Int64()],
		},
	}
	if platform == PlatformMacOS {
		// With a macOS platform the kernel still reports a Direct3D
		// renderer; the real Apple GPU is the consistent answer.
		p.Fingerprint.DisableSpoofing = []string{"gpu"}
	}
	return p, p.Validate()
}

// randomSeed is a positive 31-bit value: the kernel documents a 32-bit
// integer and some builds parse it as signed.
func randomSeed() (uint32, error) {
	n, err := rand.Int(rand.Reader, big.NewInt(1<<31-1))
	if err != nil {
		return 0, err
	}
	return uint32(n.Int64()) + 1, nil
}
