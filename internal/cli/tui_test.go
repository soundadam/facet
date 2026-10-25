package cli

import (
	"path/filepath"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/zalando/go-keyring"

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
		{map[string]string{"name": "a", "language": "en_US"}, "BCP 47"},
		{map[string]string{"name": "a", "proxy": "ftp://h:1"}, "scheme"},
	} {
		f := newProfileForm(nil, "", 80)
		fill(f, tc.values)
		if _, err := app.saveForm(f); err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("%v: err %v, want %q", tc.values, err, tc.want)
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
