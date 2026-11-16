package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"os/user"

	appkit "github.com/soundadam/appkit/go"
	appcli "github.com/soundadam/appkit/go/cli"

	"github.com/soundadam/facet/internal/cli"
	"github.com/soundadam/facet/internal/profile"
)

// version is the release version; release builds set it with
// -ldflags "-X main.version=<ver>" from the tag.
var version = "dev"

func main() {
	identity := appkit.App{Name: "facet", Version: version, Repo: "soundadam/facet"}

	// The Homebrew cask sets FACET_ACCOUNT_HOME: its install steps run with
	// HOME pointing at a scratch directory, and facet must put the kernel it
	// downloads in the user's real one. Install steps cannot name that
	// directory, so look it up from the account.
	if os.Getenv("FACET_ACCOUNT_HOME") != "" {
		if u, err := user.Current(); err == nil && u.HomeDir != "" {
			os.Setenv("HOME", u.HomeDir)
		}
	}

	root, err := profile.DefaultRoot()
	if err != nil {
		fmt.Fprintf(os.Stderr, "facet: %v\n", err)
		os.Exit(1)
	}

	app := &cli.App{
		In:        os.Stdin,
		Out:       os.Stdout,
		Err:       os.Stderr,
		StdinTTY:  isTerminal(os.Stdin),
		StdoutTTY: isTerminal(os.Stdout),
		Identity:  identity,
		Store:     profile.NewStore(root),
	}

	notice := appcli.StartNotice(identity)
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	exitCode := app.Execute(ctx, os.Args[1:])
	stop()
	notice.Print(os.Stderr)
	os.Exit(exitCode)
}

func isTerminal(file *os.File) bool {
	info, err := file.Stat()
	return err == nil && info.Mode()&os.ModeCharDevice != 0
}
