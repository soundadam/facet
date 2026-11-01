package cli

import (
	"fmt"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/zalando/go-keyring"

	"github.com/soundadam/facet/internal/launch"
	"github.com/soundadam/facet/internal/profile"
	"github.com/soundadam/facet/internal/secret"
)

var testNow = time.Date(2026, 10, 25, 8, 0, 0, 0, time.UTC)

func testApp(t *testing.T) *App {
	t.Helper()
	keyring.MockInit()
	dir := t.TempDir()
	return &App{Store: &profile.Store{Root: filepath.Join(dir, "home"), Trash: filepath.Join(dir, "trash")}}
}

func fill(f *uiForm, values map[string]string) {
	for i := range f.fields {
		if v, ok := values[f.fields[i].key]; ok {
			f.fields[i].input.SetValue(v)
		}
	}
}

func TestFormCreateAndEdit(t *testing.T) {
	app := testApp(t)
	f := newProfileForm(nil, "", 80)
	fill(f, map[string]string{
		"name":     "shop-us",
		"platform": "windows",
		"proxy":    "socks5://alice@203.0.113.7:1080",
		"password": "s3cret",
		"timezone": "America/New_York",
		"language": "en-US",
		"urls":     "https://a.example/  https://b.example/",
	})
	p, err := app.saveForm(f)
	if err != nil {
		t.Fatal(err)
	}
	if p.Serial != 1 || p.Fingerprint.Platform != "windows" || p.Proxy.Username != "alice" || len(p.StartURLs) != 2 {
		t.Fatalf("created %+v", p)
	}
	if pw, _ := secret.ProxyPassword(p.ID); pw != "s3cret" {
		t.Fatalf("password %q", pw)
	}

	// An empty password field keeps the stored one for the same user.
	f = newProfileForm(p, passwordStatus(p), 80)
	fill(f, map[string]string{"name": "shop-us-2", "timezone": "Europe/London"})
	if _, err := app.saveForm(f); err != nil {
		t.Fatal(err)
	}
	got, err := app.Store.Load(p.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Name != "shop-us-2" || got.Locale.Timezone != "Europe/London" || got.Fingerprint.Seed != p.Fingerprint.Seed {
		t.Fatalf("edited %+v", got)
	}
	if pw, _ := secret.ProxyPassword(p.ID); pw != "s3cret" {
		t.Fatalf("password after edit %q", pw)
	}

	// Going direct drops the password.
	f = newProfileForm(got, "", 80)
	fill(f, map[string]string{"proxy": ""})
	if _, err := app.saveForm(f); err != nil {
		t.Fatal(err)
	}
	if pw, _ := secret.ProxyPassword(p.ID); pw != "" {
		t.Fatalf("password kept after going direct: %q", pw)
	}
}

func TestFormRejectsBadInput(t *testing.T) {
	app := testApp(t)
	for _, tc := range []struct {
		values map[string]string
		want   string
	}{
		{map[string]string{"name": ""}, "non-empty"},
		{map[string]string{"name": "a", "timezone": "Mars/Base"}, "IANA"},
		{map[string]string{"name": "a", "language": "en US!"}, "BCP 47"},
		{map[string]string{"name": "a", "proxy": "ftp://h:1"}, "scheme"},
	} {
		f := newProfileForm(nil, "", 80)
		fill(f, tc.values)
		if _, err := app.saveForm(f); err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("%v: err %v, want %q", tc.values, err, tc.want)
		}
	}
}

func TestFormPresets(t *testing.T) {
	app := testApp(t)
	f := newProfileForm(nil, "", 80)
	fill(f, map[string]string{"name": "a"})
	tz := slices.IndexFunc(f.fields, func(fl uiField) bool { return fl.key == "timezone" })
	f.focus = tz
	m := &uiModel{app: app, mode: modeForm, form: f}
	right := tea.KeyPressMsg{Code: tea.KeyRight}
	left := tea.KeyPressMsg{Code: tea.KeyLeft}
	m.Update(right)
	if got := f.value("timezone"); got != zonePresets[0].zone {
		t.Fatalf("→ from empty gave %q", got)
	}
	m.Update(left)
	m.Update(left)
	if got := f.value("timezone"); got != "UTC" {
		t.Fatalf("← wraps to %q", got)
	}
	// A typed value is left to the text input.
	f.fields[tz].input.SetValue("Las Vegas")
	m.Update(right)
	if got := f.value("timezone"); got != "Las Vegas" {
		t.Fatalf("→ changed a typed value to %q", got)
	}
	if d := describeZoneField("Las Vegas"); !strings.HasPrefix(d, "→ America/Los_Angeles") {
		t.Fatalf("describe = %q", d)
	}

	fill(f, map[string]string{"language": "中文"})
	p, err := app.saveForm(f)
	if err != nil {
		t.Fatal(err)
	}
	if p.Locale.Timezone != "America/Los_Angeles" || p.Locale.Language != "zh-CN" {
		t.Fatalf("saved locale %+v", p.Locale)
	}
}

func TestResolveLocale(t *testing.T) {
	for in, want := range map[string]string{
		"":                  "",
		"America/New_York":  "America/New_York",
		"asia/tokyo":        "Asia/Tokyo",
		"America/Las_Vegas": "America/Los_Angeles",
		"las vegas":         "America/Los_Angeles",
		"New York":          "America/New_York",
		"singapore":         "Asia/Singapore",
		"Japan":             "Asia/Tokyo",
		"纽约":                "America/New_York",
		"Europe/Warsaw":     "Europe/Warsaw",
		"utc":               "UTC",
	} {
		if got, err := resolveTimezone(in); err != nil || got != want {
			t.Errorf("resolveTimezone(%q) = %q, %v; want %q", in, got, err, want)
		}
	}
	for _, in := range []string{"Mars/Base", "Local", "Atlantis"} {
		if _, err := resolveTimezone(in); err == nil {
			t.Errorf("resolveTimezone(%q) accepted", in)
		}
	}
	for in, want := range map[string]string{
		"":           "",
		"en-US":      "en-US",
		"en_us":      "en-US",
		"EN":         "en",
		"zh-hant-tw": "zh-Hant-TW",
		"es-419":     "es-419",
		"English":    "en-US",
		"中文":         "zh-CN",
		"繁體中文":       "zh-TW",
		"japanese":   "ja-JP",
	} {
		if got, err := resolveLanguage(in); err != nil || got != want {
			t.Errorf("resolveLanguage(%q) = %q, %v; want %q", in, got, err, want)
		}
	}
	for _, in := range []string{"en US", "e", "1x-US", "en--US", "Klingon!"} {
		if _, err := resolveLanguage(in); err == nil {
			t.Errorf("resolveLanguage(%q) accepted", in)
		}
	}
}

func TestListKeys(t *testing.T) {
	app := testApp(t)
	for _, name := range []string{"one", "two"} {
		p, err := profile.New(name, profile.PlatformLinux, testNow)
		if err != nil {
			t.Fatal(err)
		}
		if err := app.Store.Create(p); err != nil {
			t.Fatal(err)
		}
	}
	m := &uiModel{app: app, busy: map[string]string{}}
	m.Update(m.load()())
	if len(m.rows) != 2 {
		t.Fatalf("%d rows", len(m.rows))
	}
	press := func(k string) {
		code := []rune(k)[0]
		m.Update(tea.KeyPressMsg{Code: code, Text: k})
	}
	press("j")
	if m.cursor != 1 {
		t.Fatalf("cursor %d", m.cursor)
	}
	press("d")
	if m.mode != modeConfirm {
		t.Fatal("delete did not ask")
	}
	press("n") // anything but y cancels
	if m.mode != modeList || len(m.rows) != 2 {
		t.Fatalf("mode %v rows %d", m.mode, len(m.rows))
	}
	press("d")
	_, cmd := m.Update(tea.KeyPressMsg{Code: 'y', Text: "y"})
	m.Update(cmd()) // doneMsg, which asks for a reload
	m.Update(m.load()())
	if len(m.rows) != 1 || m.rows[0].p.Name != "one" {
		t.Fatalf("after delete: %+v", m.rows)
	}
	if !strings.Contains(m.View().Content, "one") {
		t.Fatal("view lacks the remaining profile")
	}
}

func TestOpenWithoutKernelOffersDownload(t *testing.T) {
	app := testApp(t)
	t.Setenv("FACET_CHROMIUM", "")
	p, err := profile.New("one", profile.PlatformLinux, testNow)
	if err != nil {
		t.Fatal(err)
	}
	if err := app.Store.Create(p); err != nil {
		t.Fatal(err)
	}
	m := &uiModel{app: app, busy: map[string]string{}}
	m.Update(m.load()())
	missing := fmt.Errorf("%w: fingerprint-chromium not found", launch.ErrNoKernel)
	m.Update(doneMsg{id: m.rows[0].p.ID, err: missing})
	if m.mode != modeConfirm || !strings.Contains(m.confirm.prompt, "Download") {
		t.Fatalf("mode %v, confirm %+v", m.mode, m.confirm)
	}
	m.download.total.Store(200_000_000)
	m.download.done.Store(50_000_000)
	if s := m.downloadStatus(); !strings.Contains(s, "25% of 200 MB") {
		t.Fatalf("download status %q", s)
	}
	m.Update(tea.KeyPressMsg{Code: 'n', Text: "n"})
	if m.mode != modeList || m.download != nil {
		t.Fatalf("cancel left mode %v, download %v", m.mode, m.download)
	}
}
