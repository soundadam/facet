package cli

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestKernelInstallIfMissing(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("fake kernels are shell scripts")
	}
	app := testApp(t)
	var out, errOut bytes.Buffer
	app.Out, app.Err = &out, &errOut
	dir := t.TempDir()

	// A usable kernel: nothing is downloaded.
	fake := filepath.Join(dir, "chrome")
	if err := os.WriteFile(fake, []byte("#!/bin/sh\necho 'Chromium 148.0.7778.215 '\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("FACET_CHROMIUM", fake)
	if code := app.Execute(context.Background(), []string{"kernel", "install", "--if-missing"}); code != 0 {
		t.Fatalf("exit %d: %s", code, errOut.String())
	}
	if !strings.Contains(out.String(), "148.0.7778.215 is already usable") {
		t.Fatalf("out %q", out.String())
	}

	// A broken FACET_CHROMIUM is reported instead of downloading around it.
	out.Reset()
	errOut.Reset()
	t.Setenv("FACET_CHROMIUM", filepath.Join(dir, "missing"))
	if code := app.Execute(context.Background(), []string{"kernel", "install", "--if-missing"}); code == 0 {
		t.Fatalf("broken FACET_CHROMIUM accepted: %s", out.String())
	}
	if !strings.Contains(errOut.String(), "FACET_CHROMIUM") || strings.Contains(errOut.String(), "downloading") {
		t.Fatalf("err %q", errOut.String())
	}

	errOut.Reset()
	if code := app.Execute(context.Background(), []string{"kernel", "install", "--if-missing", "--force"}); code == 0 {
		t.Fatal("--if-missing with --force accepted")
	}
}
