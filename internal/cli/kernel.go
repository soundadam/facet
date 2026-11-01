package cli

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"os"
	"runtime"
	"strings"

	"github.com/spf13/cobra"
	"golang.org/x/term"

	"github.com/soundadam/facet/internal/kernel"
	"github.com/soundadam/facet/internal/launch"
)

func (app *App) newKernelCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "kernel",
		Short: "Show the fingerprint-chromium kernel and the latest upstream release",
		Long: `Kernel shows which fingerprint-chromium facet launches and whether upstream
has a newer build for this machine. facet kernel install downloads one.`,
		GroupID: groupConfig,
		Args:    cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return app.kernelStatus(cmd.Context())
		},
	}
	cmd.AddCommand(app.newKernelInstallCommand())
	return cmd
}

func (app *App) newKernelInstallCommand() *cobra.Command {
	var force bool
	cmd := &cobra.Command{
		Use:   "install [VERSION | FILE]",
		Short: "Download fingerprint-chromium from its GitHub releases",
		Long: `Install downloads upstream's fingerprint-chromium build for this machine from
https://github.com/` + launch.UpstreamRepo + `/releases, checks its SHA-256 and
unpacks it into facet's own directory, where facet looks first. Without
VERSION it takes the newest release that has a build for this platform;
VERSION may be a full version or a major such as 148.

FILE installs an archive you downloaded yourself (the .dmg on macOS, the
.tar.xz on Linux, the .zip on Windows), e.g. through a mirror. It is checked
against upstream's SHA-256 when GitHub can be reached.

Profiles must be stopped first. FACET_CHROMIUM, when set, still wins.`,
		Example: `  facet kernel install
  facet kernel install 148
  facet kernel install ~/Downloads/ungoogled-chromium_148.0.7778.215-1.1_macos.dmg`,
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			arg := ""
			if len(args) == 1 {
				arg = args[0]
			}
			k, err := app.installKernel(cmd.Context(), arg, force, app.progressPrinter(), func(format string, a ...any) {
				fmt.Fprintf(app.Err, format+"\n", a...)
			})
			if err != nil || k == nil {
				return err
			}
			fmt.Fprintf(app.Out, "installed fingerprint-chromium %s in %s\n", k.Version, launch.ManagedDir())
			if v := os.Getenv("FACET_CHROMIUM"); v != "" {
				fmt.Fprintf(app.Err, "note: FACET_CHROMIUM=%s still takes precedence; unset it to use this kernel\n", v)
			}
			return nil
		},
	}
	cmd.Flags().BoolVar(&force, "force", false, "reinstall even when that version is already installed")
	return cmd
}

func (app *App) kernelStatus(ctx context.Context) error {
	k, err := launch.Locate(ctx)
	switch {
	case k != nil:
		fmt.Fprintf(app.Out, "kernel    fingerprint-chromium %s\n", k.Version)
		fmt.Fprintf(app.Out, "path      %s%s\n", k.Exe, kernelSource(k))
		if err != nil {
			fmt.Fprintf(app.Out, "problem   %s\n", err)
		}
	default:
		fmt.Fprintf(app.Out, "kernel    none: %s\n", err)
	}

	platform := kernel.PlatformName(runtime.GOOS, runtime.GOARCH)
	releases, rerr := kernel.Releases(ctx)
	if rerr != nil {
		fmt.Fprintf(app.Out, "upstream  unknown (%s)\n", rerr)
		return nil
	}
	r, a, perr := kernel.Pick(releases, "", runtime.GOOS, runtime.GOARCH)
	if perr != nil {
		fmt.Fprintf(app.Out, "upstream  %s\n", perr)
		return nil
	}
	state := "run `facet kernel install` to get it"
	if k != nil && k.Version == r.Version {
		state = "up to date"
	} else if k != nil && launch.VersionLess(k.Version, r.Version) {
		state = "newer; `facet kernel install` updates"
	} else if k != nil {
		state = "yours is newer"
	}
	fmt.Fprintf(app.Out, "upstream  %s for %s, %s (%s)\n", r.Version, platform, sizeMB(a.Size), state)
	if newest := releases[0]; newest.Version != r.Version {
		fmt.Fprintf(app.Out, "          %s is out, without a %s build yet\n", newest.Version, platform)
	}
	return nil
}

// kernelSource says why Locate chose k.
func kernelSource(k *launch.Kernel) string {
	switch {
	case os.Getenv("FACET_CHROMIUM") != "":
		return " (from FACET_CHROMIUM)"
	case strings.HasPrefix(k.Exe, launch.ManagedDir()+string(os.PathSeparator)):
		return " (installed by facet)"
	}
	return ""
}

// installKernel installs arg, a release version ("" for the newest) or an
// archive on disk, into launch.ManagedDir. It returns nil and no error when
// that version is already there and force is off. note reports steps.
func (app *App) installKernel(ctx context.Context, arg string, force bool, progress kernel.Progress, note func(string, ...any)) (*launch.Kernel, error) {
	if err := app.checkNoneRunning(); err != nil {
		return nil, err
	}
	if isArchive(arg) {
		releases, err := kernel.Releases(ctx)
		digest, ok := kernel.Digest(releases, baseName(arg))
		switch {
		case err != nil:
			note("warning: cannot reach GitHub to look up the SHA-256 of %s (%s); installing it unchecked", arg, err)
		case !ok:
			note("warning: %s is not a file of any upstream release that GitHub lists; installing it unchecked", baseName(arg))
		default:
			note("checking %s against upstream's SHA-256", baseName(arg))
		}
		note("unpacking %s…", baseName(arg))
		return kernel.InstallFile(ctx, arg, digest)
	}

	releases, err := kernel.Releases(ctx)
	if err != nil {
		return nil, fmt.Errorf("%w\nIf GitHub is blocked here, download the archive some other way and run `facet kernel install FILE`", err)
	}
	r, a, err := kernel.Pick(releases, arg, runtime.GOOS, runtime.GOARCH)
	if err != nil {
		return nil, err
	}
	if !force {
		if cur, err := launch.Inspect(ctx, launch.ManagedExe(launch.ManagedDir())); err == nil && cur.Version == r.Version {
			note("fingerprint-chromium %s is already installed in %s (--force reinstalls)", cur.Version, launch.ManagedDir())
			return nil, nil
		}
	}
	note("downloading fingerprint-chromium %s for %s (%s)", r.Version, kernel.PlatformName(runtime.GOOS, runtime.GOARCH), sizeMB(a.Size))
	note("  %s", a.URL)
	if a.Digest == "" {
		note("warning: upstream published no SHA-256 for %s; it will not be checked", a.Name)
	}
	return kernel.Install(ctx, a, progress)
}

// baseName is the file name in a path written with / or \.
func baseName(path string) string {
	return path[strings.LastIndexAny(path, `/\`)+1:]
}

// isArchive tells a file argument from a version.
func isArchive(arg string) bool {
	if arg == "" {
		return false
	}
	if info, err := os.Stat(arg); err == nil && !info.IsDir() {
		return true
	}
	lower := strings.ToLower(arg)
	return strings.HasSuffix(lower, ".dmg") || strings.HasSuffix(lower, ".zip") || strings.HasSuffix(lower, ".tar.xz")
}

// checkNoneRunning refuses to replace the kernel under a running browser.
func (app *App) checkNoneRunning() error {
	profiles, err := app.Store.List()
	if err != nil {
		return err
	}
	var running []string
	for _, p := range profiles {
		if st, _ := app.Store.RunState(p.ID); st != nil {
			running = append(running, fmt.Sprintf("#%d %s", p.Serial, p.Name))
		}
	}
	if len(running) > 0 {
		return fmt.Errorf("stop the running profiles first (%s): their browsers use the current kernel", strings.Join(running, ", "))
	}
	return nil
}

// progressPrinter shows a download's progress on stderr when it is a
// terminal.
func (app *App) progressPrinter() kernel.Progress {
	f, ok := app.Err.(*os.File)
	if !ok || !term.IsTerminal(int(f.Fd())) {
		return nil
	}
	last := -1
	return func(done, total int64) {
		if total <= 0 {
			return
		}
		pct := int(done * 100 / total)
		if pct == last {
			return
		}
		last = pct
		fmt.Fprintf(f, "\rDownloading… %3d%%", pct)
		if done >= total {
			fmt.Fprint(f, "\nUnpacking…\n")
		}
	}
}

// ensureKernel returns the kernel, offering to download one when there is
// none and someone is at the terminal to say yes.
func (app *App) ensureKernel(ctx context.Context) (*launch.Kernel, error) {
	k, err := launch.Locate(ctx)
	if err == nil || !errors.Is(err, launch.ErrNoKernel) || !app.StdinTTY || !app.StdoutTTY || os.Getenv("FACET_CHROMIUM") != "" {
		return k, err
	}
	fmt.Fprintln(app.Err, err)
	releases, rerr := kernel.Releases(ctx)
	if rerr != nil {
		return nil, err
	}
	r, a, perr := kernel.Pick(releases, "", runtime.GOOS, runtime.GOARCH)
	if perr != nil {
		return nil, fmt.Errorf("%w\n%w", err, perr)
	}
	fmt.Fprintf(app.Err, "Download fingerprint-chromium %s for %s (%s) from github.com/%s now? [Y/n] ",
		r.Version, kernel.PlatformName(runtime.GOOS, runtime.GOARCH), sizeMB(a.Size), launch.UpstreamRepo)
	answer, _ := bufio.NewReader(app.In).ReadString('\n')
	if a := strings.ToLower(strings.TrimSpace(answer)); a != "" && a != "y" && a != "yes" {
		return nil, err
	}
	installed, ierr := app.installKernel(ctx, r.Version, true, app.progressPrinter(), func(format string, a ...any) {
		fmt.Fprintf(app.Err, format+"\n", a...)
	})
	if ierr != nil {
		return nil, ierr
	}
	fmt.Fprintf(app.Err, "installed fingerprint-chromium %s in %s\n", installed.Version, launch.ManagedDir())
	return launch.Locate(ctx)
}

func sizeMB(n int64) string {
	if n <= 0 {
		return "size unknown"
	}
	return fmt.Sprintf("%d MB", (n+500_000)/1_000_000)
}
