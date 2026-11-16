package kernel

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
)

func TestPending(t *testing.T) {
	t.Setenv("FACET_HOME", t.TempDir())
	ctx := context.Background()
	b := fakeArchive(t, "148.0.7778.215")
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.Write(b) }))
	defer srv.Close()
	a := Asset{Name: "fp_macos.zip", URL: srv.URL + "/fp.zip", Size: int64(len(b)), Digest: digestOf(b)}

	if _, _, ok := Pending(); ok {
		t.Fatal("pending before any download")
	}
	if _, err := InstallPending(ctx); !os.IsNotExist(err) {
		t.Fatalf("InstallPending with none: %v", err)
	}

	// A tampered download leaves nothing pending.
	bad := a
	bad.Digest = digestOf([]byte("other"))
	if _, err := Download(ctx, bad, nil); err == nil || !strings.Contains(err.Error(), "SHA-256") {
		t.Fatalf("tampered download: %v", err)
	}
	if _, _, ok := Pending(); ok {
		t.Fatal("tampered download left pending")
	}

	path, err := Download(ctx, a, nil)
	if err != nil {
		t.Fatal(err)
	}
	if p, d, ok := Pending(); !ok || p != path || d != a.Digest {
		t.Fatalf("Pending = %q %q %v, want %q %q", p, d, ok, path, a.Digest)
	}
	// A second download replaces the first.
	if path, err = Download(ctx, a, nil); err != nil {
		t.Fatal(err)
	}
	k, err := InstallPending(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if k.Version != "148.0.7778.215" {
		t.Fatalf("installed %+v", k)
	}
	if _, err := os.Stat(PendingDir()); !os.IsNotExist(err) {
		t.Fatalf("pending dir left after install: %v", err)
	}

	// A pending archive whose digest no longer matches is dropped.
	if _, err := Download(ctx, a, nil); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path+".sha256", []byte(digestOf([]byte("other"))+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := InstallPending(ctx); err == nil {
		t.Fatal("bad digest installed")
	}
	if _, _, ok := Pending(); ok {
		t.Fatal("archive with bad digest kept")
	}
}
