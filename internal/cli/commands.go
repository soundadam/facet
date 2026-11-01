package cli

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"log"
	"os"
	"strconv"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
	"golang.org/x/term"

	"github.com/soundadam/facet/internal/launch"
	"github.com/soundadam/facet/internal/profile"
	"github.com/soundadam/facet/internal/secret"
)

// editFlags are the profile fields `new` and `set` can change. Only flags
// given on the command line are applied, so `set` leaves the rest alone.
type editFlags struct {
	name, proxy, timezone, lang, acceptLang string
	brand, brandVersion, platformVersion    string
	notes                                   string
	urls                                    []string
}

func (f *editFlags) register(flags *pflag.FlagSet, rename bool) {
	if rename {
		flags.StringVar(&f.name, "name", "", "rename the profile")
	}
	flags.StringVar(&f.proxy, "proxy", "", `upstream proxy URL (http, https, socks5, socks5h), or "direct"; a password left out of the URL is asked for`)
	flags.StringVar(&f.timezone, "timezone", "", `time zone the browser reports: an IANA name such as America/New_York or a city such as "Las Vegas", Singapore, 东京 (see facet zones; "" clears)`)
	flags.StringVar(&f.lang, "lang", "", "browser UI and navigator language: a tag such as en-US, zh-CN, ja-JP, or a name such as English, 中文")
	flags.StringVar(&f.acceptLang, "accept-lang", "", "Accept-Language list; derived from --lang when unset")
	flags.StringVar(&f.brand, "brand", "", "browser brand in the UA and client hints: Chrome, Edge, Opera, Vivaldi")
	flags.StringVar(&f.brandVersion, "brand-version", "", "brand version; the kernel's own when unset")
	flags.StringVar(&f.platformVersion, "platform-version", "", "OS version in client hints, e.g. 15.5.0")
	flags.StringVar(&f.notes, "notes", "", "free-form notes, e.g. which account this is")
	flags.StringArrayVar(&f.urls, "url", nil, "page to open on launch; repeat for several")
}

// apply copies the changed flags into p. It returns the proxy password
// to store when --proxy was given (keep is true when the stored one stays).
func (app *App) apply(cmd *cobra.Command, p *profile.Profile, f *editFlags) (proxyChanged bool, password string, keep bool, err error) {
	changed := cmd.Flags().Changed
	if changed("name") {
		if err := profile.ValidateName(f.name); err != nil {
			return false, "", false, err
		}
		p.Name = f.name
	}
	if changed("timezone") {
		tz, err := resolveTimezone(f.timezone)
		if err != nil {
			return false, "", false, err
		}
		app.noteResolved(f.timezone, tz)
		p.Locale.Timezone = tz
	}
	if changed("lang") {
		lang, err := resolveLanguage(f.lang)
		if err != nil {
			return false, "", false, err
		}
		app.noteResolved(f.lang, lang)
		p.Locale.Language = lang
	}
	if changed("accept-lang") {
		p.Locale.AcceptLanguage = strings.ReplaceAll(f.acceptLang, " ", "")
	}
	if changed("brand") {
		p.Fingerprint.Brand = f.brand
	}
	if changed("brand-version") {
		p.Fingerprint.BrandVersion = f.brandVersion
	}
	if changed("platform-version") {
		p.Fingerprint.PlatformVersion = f.platformVersion
	}
	if changed("notes") {
		p.Notes = f.notes
	}
	if changed("url") {
		p.StartURLs = f.urls
	}
	if changed("proxy") {
		proxy, pw, err := profile.ParseProxy(f.proxy)
		if err != nil {
			return false, "", false, err
		}
		if proxy.Authenticated() && pw == "" {
			existing := p.ID != "" && p.Proxy.Username == proxy.Username
			if pw, err = app.readPassword(proxy, existing); err != nil {
				return false, "", false, err
			}
			keep = existing && pw == ""
		} else if pw != "" && app.StdinTTY {
			fmt.Fprintln(app.Err, "note: the password is now in your shell history; leave it out of --proxy to be asked instead")
		}
		p.Proxy = proxy
		return true, pw, keep, nil
	}
	return false, "", false, nil
}

// noteResolved says what a city or language name was taken to mean.
func (app *App) noteResolved(typed, resolved string) {
	if typed = strings.TrimSpace(typed); typed != resolved {
		fmt.Fprintf(app.Err, "note: %s → %s\n", typed, resolved)
	}
}

// readPassword asks for the proxy password on the terminal, or reads one
// line from stdin when it is not a terminal.
func (app *App) readPassword(proxy profile.Proxy, existing bool) (string, error) {
	if f, ok := app.In.(*os.File); ok && app.StdinTTY {
		hint := ""
		if existing {
			hint = " (Enter keeps the stored one)"
		}
		fmt.Fprintf(app.Err, "Password for %s%s: ", proxy, hint)
		b, err := term.ReadPassword(int(f.Fd()))
		fmt.Fprintln(app.Err)
		return string(b), err
	}
	line, err := bufio.NewReader(app.In).ReadString('\n')
	if err != nil && line == "" {
		return "", nil
	}
	return strings.TrimRight(line, "\r\n"), nil
}

func (app *App) storePassword(p *profile.Profile, password string, keep bool) error {
	if keep {
		return nil
	}
	if !p.Proxy.Authenticated() {
		password = ""
	}
	if err := secret.SetProxyPassword(p.ID, password); err != nil {
		return fmt.Errorf("store the proxy password in the keychain: %w (set it again with `facet set %d --proxy ...`)", err, p.Serial)
	}
	return nil
}

func (app *App) newNewCommand() *cobra.Command {
	var f editFlags
	var platform string
	cmd := &cobra.Command{
		Use:   "new NAME",
		Short: "Create a profile with a fresh fingerprint",
		Long: `New creates a profile: a random fingerprint seed, a plausible core count for
the platform, and an empty browser data directory. The seed and platform are
the profile's identity and stay fixed; make another profile for another one.

--platform defaults to this machine's, which keeps fonts and the GPU
consistent with what the browser claims.`,
		Example: `  facet new shop-us --proxy socks5://alice@203.0.113.7:1080 --timezone America/New_York --lang en-US
  facet new mail-2 --notes "second mailbox" --url https://mail.example.com/`,
		GroupID: groupProfiles,
		Args:    cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			p, err := profile.New(args[0], platform, time.Now())
			if err != nil {
				return err
			}
			_, password, _, err := app.apply(cmd, p, &f)
			if err != nil {
				return err
			}
			if err := app.Store.Create(p); err != nil {
				return err
			}
			if password != "" {
				if err := app.storePassword(p, password, false); err != nil {
					return err
				}
			}
			fmt.Fprintf(app.Out, "created #%d %s (%s, seed %d)\n", p.Serial, p.Name, p.Fingerprint.Platform, p.Fingerprint.Seed)
			fmt.Fprintf(app.Out, "open it with: facet open %d\n", p.Serial)
			return nil
		},
	}
	cmd.Flags().StringVar(&platform, "platform", hostPlatform(), "platform the browser claims: macos, windows or linux")
	f.register(cmd.Flags(), false)
	return cmd
}

func (app *App) newSetCommand() *cobra.Command {
	var f editFlags
	cmd := &cobra.Command{
		Use:   "set PROFILE",
		Short: "Change a profile's proxy, locale, name or start pages",
		Long: `Set changes only the fields given as flags. PROFILE is a serial (3 or #3),
a name or an ID. Changes apply the next time the profile is opened.`,
		Example: `  facet set 1 --proxy http://bob@198.51.100.2:3128
  facet set shop-us --timezone Europe/London --lang en-GB
  facet set 2 --proxy direct`,
		GroupID: groupProfiles,
		Args:    cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if cmd.Flags().NFlag() == 0 {
				return errors.New("nothing to change; see `facet set --help` for the flags")
			}
			p, err := app.Store.Resolve(args[0])
			if err != nil {
				return err
			}
			if cmd.Flags().Changed("name") && !strings.EqualFold(f.name, p.Name) {
				if other, err := app.Store.Resolve(f.name); err == nil && other.ID != p.ID {
					return fmt.Errorf("a profile named %q already exists (#%d)", other.Name, other.Serial)
				}
			}
			proxyChanged, password, keep, err := app.apply(cmd, p, &f)
			if err != nil {
				return err
			}
			if err := app.Store.Save(p); err != nil {
				return err
			}
			if proxyChanged {
				if err := app.storePassword(p, password, keep); err != nil {
					return err
				}
			}
			fmt.Fprintf(app.Out, "updated #%d %s\n", p.Serial, p.Name)
			if state, _ := app.Store.RunState(p.ID); state != nil {
				fmt.Fprintln(app.Out, "it is running; close it and open it again for the change to apply")
			}
			return nil
		},
	}
	f.register(cmd.Flags(), true)
	return cmd
}

func (app *App) newListCommand() *cobra.Command {
	return &cobra.Command{
		Use:     "ls",
		Aliases: []string{"list"},
		Short:   "List profiles",
		GroupID: groupProfiles,
		Args:    cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return app.list(cmd.Context())
		},
	}
}

func (app *App) list(context.Context) error {
	profiles, err := app.Store.List()
	if len(profiles) == 0 {
		if err != nil {
			return err
		}
		fmt.Fprintln(app.Out, "no profiles yet; create one with: facet new NAME")
		return nil
	}
	tw := tabwriter.NewWriter(app.Out, 0, 4, 2, ' ', 0)
	fmt.Fprintln(tw, "#\tNAME\tPLATFORM\tPROXY\tTIMEZONE\tSTATE")
	for _, p := range profiles {
		state := ""
		if st, _ := app.Store.RunState(p.ID); st != nil {
			state = runningText(st)
		}
		fmt.Fprintf(tw, "%d\t%s\t%s\t%s\t%s\t%s\n", p.Serial, p.Name, p.Fingerprint.Platform, p.Proxy, dash(p.Locale.Timezone), state)
	}
	if err := tw.Flush(); err != nil {
		return err
	}
	// Unreadable profiles are reported after the ones that loaded.
	return err
}

func runningText(st *profile.RunState) string {
	if st.Orphaned() && st.Bridge != "" {
		return "running, bridge down"
	}
	return "running"
}

func dash(s string) string {
	if s == "" {
		return "-"
	}
	return s
}

func (app *App) newShowCommand() *cobra.Command {
	return &cobra.Command{
		Use:     "show PROFILE",
		Short:   "Show a profile's settings and paths",
		GroupID: groupProfiles,
		Args:    cobra.ExactArgs(1),
		RunE: func(_ *cobra.Command, args []string) error {
			p, err := app.Store.Resolve(args[0])
			if err != nil {
				return err
			}
			fp, loc := p.Fingerprint, p.Locale
			tw := tabwriter.NewWriter(app.Out, 0, 4, 2, ' ', 0)
			row := func(k, v string) {
				if v != "" {
					fmt.Fprintf(tw, "%s\t%s\n", k, v)
				}
			}
			row("profile", fmt.Sprintf("#%d %s (id %s)", p.Serial, p.Name, p.ID))
			row("notes", p.Notes)
			row("platform", strings.TrimSpace(fp.Platform+" "+fp.PlatformVersion))
			row("seed", strconv.FormatUint(uint64(fp.Seed), 10))
			row("brand", strings.TrimSpace(fp.Brand+" "+fp.BrandVersion))
			row("cores", strconv.Itoa(fp.HardwareConcurrency))
			if len(fp.DisableSpoofing) > 0 {
				row("not spoofed", strings.Join(fp.DisableSpoofing, ", "))
			}
			proxy := p.Proxy.String()
			if p.Proxy.Authenticated() {
				pw, err := secret.ProxyPassword(p.ID)
				switch {
				case err != nil:
					proxy += "  (keychain: " + err.Error() + ")"
				case pw == "":
					proxy += "  (no password stored)"
				default:
					proxy += "  (password in keychain, via local bridge)"
				}
			}
			row("proxy", proxy)
			row("timezone", dash(loc.Timezone))
			row("language", dash(loc.Language))
			accept := loc.AcceptLanguage
			if accept == "" && loc.Language != "" {
				accept = launch.AcceptLanguage(loc.Language) + " (derived)"
			}
			row("accept-lang", accept)
			row("start pages", strings.Join(p.StartURLs, " "))
			row("created", p.Created.Local().Format("2006-01-02 15:04"))
			row("data", app.Store.Dir(p.ID))
			if st, _ := app.Store.RunState(p.ID); st != nil {
				state := fmt.Sprintf("%s since %s (browser pid %d)", runningText(st), st.Started.Local().Format("15:04"), st.BrowserPID)
				if st.Bridge != "" {
					state += ", bridge " + st.Bridge
				}
				row("state", state)
			}
			return tw.Flush()
		},
	}
}

func (app *App) newRemoveCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "rm PROFILE",
		Short: "Move a profile and its browser data to the trash",
		Long: `Rm moves the profile directory, cookies and logins included, to the
trash, and deletes its proxy password from the keychain.`,
		GroupID: groupProfiles,
		Args:    cobra.ExactArgs(1),
		RunE: func(_ *cobra.Command, args []string) error {
			p, err := app.Store.Resolve(args[0])
			if err != nil {
				return err
			}
			dest, err := app.Store.Remove(p)
			if err != nil {
				return err
			}
			if err := secret.DeleteProxyPassword(p.ID); err != nil {
				fmt.Fprintf(app.Err, "warning: proxy password not removed from the keychain: %v\n", err)
			}
			fmt.Fprintf(app.Out, "moved #%d %s to %s\n", p.Serial, p.Name, dest)
			return nil
		},
	}
}

func (app *App) newWipeCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "wipe PROFILE",
		Short: "Clear a profile's cookies and browsing data, keeping its fingerprint",
		Long: `Wipe moves the profile's browser data and caches (cookies, logins,
history, local storage, extensions) to the trash. The seed, proxy and locale
stay, so the next launch is the same identity starting from a fresh install.`,
		GroupID: groupProfiles,
		Args:    cobra.ExactArgs(1),
		RunE: func(_ *cobra.Command, args []string) error {
			p, err := app.Store.Resolve(args[0])
			if err != nil {
				return err
			}
			dest, err := app.Store.Wipe(p)
			if err != nil {
				return err
			}
			fmt.Fprintf(app.Out, "wiped #%d %s; old data is in %s\n", p.Serial, p.Name, dest)
			return nil
		},
	}
}

func (app *App) newOpenCommand() *cobra.Command {
	var foreground bool
	cmd := &cobra.Command{
		Use:   "open PROFILE [URL...]",
		Short: "Open a profile's browser",
		Long: `Open starts the profile's browser in the background and returns once it is
up. URLs replace the profile's start pages for this launch. If the profile
is already running, the URLs open in a new window of that browser.

The browser's log is in the profile directory (facet show PROFILE).`,
		Example: `  facet open shop-us
  facet open 2 https://example.com/ https://example.org/`,
		GroupID: groupBrowse,
		Args:    cobra.MinimumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			p, err := app.Store.Resolve(args[0])
			if err != nil {
				return err
			}
			kernel, err := app.ensureKernel(ctx)
			if err != nil {
				return err
			}
			if foreground {
				if state, _ := app.Store.RunState(p.ID); state == nil {
					return app.supervise(ctx, p, kernel, args[1:])
				}
			}
			msg, err := app.openProfile(ctx, p, args[1:])
			if err != nil {
				return err
			}
			fmt.Fprintln(app.Out, msg)
			return nil
		},
	}
	cmd.Flags().BoolVar(&foreground, "foreground", false, "stay attached and print the browser's log until it exits")
	return cmd
}

// openProfile starts p in the background, or opens a new window when it
// is already running, and describes what happened.
func (app *App) openProfile(ctx context.Context, p *profile.Profile, urls []string) (string, error) {
	kernel, err := launch.Locate(ctx)
	if err != nil {
		return "", err
	}
	if state, _ := app.Store.RunState(p.ID); state != nil {
		if state.Orphaned() && state.Bridge != "" {
			return "", fmt.Errorf("#%d %s lost its proxy bridge, so its pages cannot load; stop it and open it again", p.Serial, p.Name)
		}
		if err := launch.NewWindow(kernel, app.Store.UserDataDir(p.ID), urls); err != nil {
			return "", err
		}
		return fmt.Sprintf("#%d %s is running; opened a new window", p.Serial, p.Name), nil
	}
	state, err := launch.Detach(ctx, app.Store, p, urls)
	if err != nil {
		return "", err
	}
	via := ""
	if state.Bridge != "" {
		via = ", proxy via bridge " + state.Bridge
	} else if !p.Proxy.Direct() {
		via = ", proxy " + p.Proxy.String()
	}
	return fmt.Sprintf("opened #%d %s (Chromium %s, pid %d%s)", p.Serial, p.Name, kernel.Version, state.BrowserPID, via), nil
}

// stopProfile quits p's browser and waits for its supervisor to clean up.
func (app *App) stopProfile(p *profile.Profile) (string, error) {
	state, err := app.Store.RunState(p.ID)
	if err != nil {
		return "", err
	}
	if state == nil {
		return fmt.Sprintf("#%d %s is not running", p.Serial, p.Name), nil
	}
	if err := launch.Stop(state); err != nil {
		return "", err
	}
	for deadline := time.Now().Add(15 * time.Second); time.Now().Before(deadline); time.Sleep(200 * time.Millisecond) {
		if st, _ := app.Store.RunState(p.ID); st == nil {
			return fmt.Sprintf("stopped #%d %s", p.Serial, p.Name), nil
		}
	}
	return "", fmt.Errorf("#%d %s did not stop within 15s (supervisor pid %d)", p.Serial, p.Name, state.SupervisorPID)
}

func (app *App) newStopCommand() *cobra.Command {
	return &cobra.Command{
		Use:     "stop PROFILE",
		Short:   "Quit a running profile's browser",
		GroupID: groupBrowse,
		Args:    cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			p, err := app.Store.Resolve(args[0])
			if err != nil {
				return err
			}
			msg, err := app.stopProfile(p)
			if err != nil {
				return err
			}
			fmt.Fprintln(app.Out, msg)
			return nil
		},
	}
}

// newRunCommand is the supervisor `open` starts in the background.
func (app *App) newRunCommand() *cobra.Command {
	return &cobra.Command{
		Use:    "run ID [-- URL...]",
		Short:  "Supervise a profile's browser (started by open)",
		Hidden: true,
		Args:   cobra.MinimumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			p, err := app.Store.Load(args[0])
			if err != nil {
				return err
			}
			kernel, err := launch.Locate(cmd.Context())
			if err != nil {
				return err
			}
			return app.supervise(cmd.Context(), p, kernel, args[1:])
		},
	}
}

func (app *App) supervise(ctx context.Context, p *profile.Profile, kernel *launch.Kernel, urls []string) error {
	if state, _ := app.Store.RunState(p.ID); state != nil {
		return fmt.Errorf("#%d %s is already running (supervisor pid %d)", p.Serial, p.Name, state.SupervisorPID)
	}
	password := ""
	if p.Proxy.Authenticated() {
		var err error
		if password, err = secret.ProxyPassword(p.ID); err != nil {
			return fmt.Errorf("read the proxy password from the keychain: %w", err)
		}
	}
	ctx, stop := launch.NotifyStop(ctx)
	defer stop()
	s := &launch.Supervisor{
		Store:    app.Store,
		Profile:  p,
		Kernel:   kernel,
		Password: password,
		URLs:     urls,
		Logger:   log.New(app.Err, "facet: ", log.LstdFlags),
		Output:   app.Err,
	}
	return s.Run(ctx)
}
