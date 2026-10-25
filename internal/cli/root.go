// Package cli is facet's command line.
package cli

import (
	"context"
	"io"
	"runtime"

	"github.com/charmbracelet/fang"
	appkit "github.com/soundadam/appkit/go"
	appcli "github.com/soundadam/appkit/go/cli"
	"github.com/spf13/cobra"

	"github.com/soundadam/facet/internal/profile"
)

// Help groups shown by `facet --help`.
const (
	groupProfiles = "profiles"
	groupBrowse   = "browse"
	groupConfig   = "config"
)

// App holds the process-wide dependencies of a facet invocation.
type App struct {
	In       io.Reader
	Out      io.Writer
	Err      io.Writer
	StdinTTY bool
	// StdoutTTY with StdinTTY makes a bare `facet` open the interactive screen.
	StdoutTTY bool
	Identity  appkit.App
	Store     *profile.Store
}

// Execute parses args and runs the selected command, returning the process
// exit code.
func (app *App) Execute(ctx context.Context, args []string) int {
	root := app.newRootCommand()
	root.SetArgs(args)
	root.SetIn(app.In)
	root.SetOut(app.Out)
	root.SetErr(app.Err)
	if err := fang.Execute(ctx, root, fang.WithoutVersion()); err != nil {
		return 1
	}
	return 0
}

func (app *App) newRootCommand() *cobra.Command {
	root := &cobra.Command{
		Use:   "facet",
		Short: "Manage fingerprint-chromium profiles for multiple accounts",
		Long: `facet keeps one fingerprint-chromium identity per account: its own seed,
cookies, proxy and locale, opened in its own window.

Run it without arguments on a terminal to manage profiles interactively;
piped, it lists them. Proxy passwords live in the system keychain; a proxy
with credentials goes through a local bridge, since Chromium cannot
authenticate to a proxy by itself.`,
		Example: `  # a profile per account, with its own exit
  facet new shop-us --proxy socks5://user@203.0.113.7:1080 --timezone America/New_York --lang en-US

  # open it (again: a new window in the running browser)
  facet open shop-us
  facet open 1 https://example.com/`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if app.StdinTTY && app.StdoutTTY {
				return app.runUI(cmd.Context())
			}
			return app.list(cmd.Context())
		},
	}
	root.AddGroup(
		&cobra.Group{ID: groupProfiles, Title: "profiles"},
		&cobra.Group{ID: groupBrowse, Title: "browse"},
		&cobra.Group{ID: groupConfig, Title: "setup"},
	)
	root.AddCommand(
		app.newNewCommand(),
		app.newSetCommand(),
		app.newListCommand(),
		app.newUICommand(),
		app.newShowCommand(),
		app.newRemoveCommand(),
		app.newWipeCommand(),
		app.newOpenCommand(),
		app.newStopCommand(),
		app.newRunCommand(),
	)
	appcli.AddCommands(root, app.Identity)
	for _, command := range root.Commands() {
		switch command.Name() {
		case "update", "version":
			command.GroupID = groupConfig
		}
	}
	root.SetHelpCommandGroupID(groupConfig)
	root.SetCompletionCommandGroupID(groupConfig)
	return root
}

// hostPlatform is the default claimed platform: matching the machine keeps
// fonts, GPU and screen consistent with what the profile says it is.
func hostPlatform() string {
	switch runtime.GOOS {
	case "darwin":
		return profile.PlatformMacOS
	case "windows":
		return profile.PlatformWindows
	default:
		return profile.PlatformLinux
	}
}
