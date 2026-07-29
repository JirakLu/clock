package main

import (
	"errors"
	"fmt"
	"os"
	"runtime/debug"
	"strings"

	appconfigure "github.com/JirakLu/clock/internal/app/configure"
	applog "github.com/JirakLu/clock/internal/app/log"
	"github.com/JirakLu/clock/internal/app/recording"
	appreport "github.com/JirakLu/clock/internal/app/report"
	"github.com/JirakLu/clock/internal/cli"
	"github.com/JirakLu/clock/internal/config"
	"github.com/JirakLu/clock/internal/credential"
	"github.com/JirakLu/clock/internal/jira"
	"time"
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
	location, err := machineLocation()
	if err != nil {
		_, _ = fmt.Fprintf(os.Stderr, "Error: locate machine IANA timezone: %v\n", err)
		return 1
	}

	prompter := cli.NewInteractivePrompter(
		os.Stdin,
		os.Stderr,
		cli.TerminalSecretReader{Input: os.Stdin},
	)
	jiraClient := jira.NewIdentityClient(nil)
	credentials := credential.NewNativeStore()
	configurations := config.NewStore(userConfigDir)
	configureService := appconfigure.New(
		jiraClient,
		prompter,
		credentials,
		configurations,
	)
	logService := applog.New(
		configurations,
		credentials,
		recording.New(jiraClient),
		time.Now,
	)
	reportService := appreport.New(
		configurations,
		credentials,
		jiraClient,
		time.Now,
		location,
	)
	root := cli.NewRoot(cli.RootOptions{
		Configure: configureService,
		Log:       logService,
		Report:    reportService,
		Prompter:  prompter,
		In:        os.Stdin,
		Out:       os.Stdout,
		Err:       os.Stderr,
		Version:   version,
		Revision:  sourceRevision(),
		Now:       time.Now,
		Location:  location,
	})
	return cli.Execute(root)
}

func machineLocation() (*time.Location, error) {
	candidates := []string{strings.TrimSpace(os.Getenv("TZ"))}
	if localName := time.Local.String(); localName != "Local" {
		candidates = append(candidates, localName)
	}
	if target, err := os.Readlink("/etc/localtime"); err == nil {
		if marker := strings.LastIndex(target, "zoneinfo/"); marker >= 0 {
			candidates = append(candidates, target[marker+len("zoneinfo/"):])
		}
	}
	if data, err := os.ReadFile("/etc/timezone"); err == nil {
		candidates = append(candidates, strings.TrimSpace(string(data)))
	}
	for _, candidate := range candidates {
		candidate = strings.TrimPrefix(candidate, ":")
		if candidate == "" || strings.HasPrefix(candidate, "/") {
			continue
		}
		location, err := time.LoadLocation(candidate)
		if err == nil && location.String() != "Local" {
			return location, nil
		}
	}
	return nil, errors.New("set TZ to an IANA name such as Europe/Prague")
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
