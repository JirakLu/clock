package main

import (
	"fmt"
	"os"
	"runtime/debug"

	appconfigure "github.com/JirakLu/clock/internal/app/configure"
	"github.com/JirakLu/clock/internal/cli"
	"github.com/JirakLu/clock/internal/config"
	"github.com/JirakLu/clock/internal/credential"
	"github.com/JirakLu/clock/internal/jira"
)

var (
	version  = "v0.1.0-dev"
	revision = "unknown"
)

func main() {
	os.Exit(run())
}

func run() int {
	userConfigDir, err := os.UserConfigDir()
	if err != nil {
		_, _ = fmt.Fprintf(os.Stderr, "Error: locate platform configuration directory: %v\n", err)
		return 1
	}

	prompter := cli.NewInteractivePrompter(
		os.Stdin,
		os.Stderr,
		cli.TerminalSecretReader{Input: os.Stdin},
	)
	configureService := appconfigure.New(
		jira.NewIdentityClient(nil),
		prompter,
		credential.NewNativeStore(),
		config.NewStore(userConfigDir),
	)
	root := cli.NewRoot(cli.RootOptions{
		Configure: configureService,
		Prompter:  prompter,
		In:        os.Stdin,
		Out:       os.Stdout,
		Err:       os.Stderr,
		Version:   version,
		Revision:  sourceRevision(),
	})
	return cli.Execute(root)
}

func sourceRevision() string {
	if revision != "unknown" {
		return revision
	}
	build, ok := debug.ReadBuildInfo()
	if !ok {
		return revision
	}
	for _, setting := range build.Settings {
		if setting.Key == "vcs.revision" && setting.Value != "" {
			return setting.Value
		}
	}
	return revision
}
