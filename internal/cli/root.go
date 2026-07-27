package cli

import (
	"context"
	"fmt"
	"io"

	appconfigure "github.com/JirakLu/clock/internal/app/configure"
	"github.com/JirakLu/clock/internal/earnings"
	"github.com/JirakLu/clock/internal/secret"
	"github.com/spf13/cobra"
)

type ConfigureRunner interface {
	Run(context.Context, appconfigure.Input) (appconfigure.Result, error)
}

type Prompter interface {
	ReadLine(string) (string, error)
	ReadSecret(string) (string, error)
}

type RootOptions struct {
	Configure ConfigureRunner
	Prompter  Prompter
	In        io.Reader
	Out       io.Writer
	Err       io.Writer
	Version   string
	Revision  string
}

func NewRoot(options RootOptions) *cobra.Command {
	version := options.Version
	if version == "" {
		version = "dev"
	}
	revision := options.Revision
	if revision == "" {
		revision = "unknown"
	}
	root := &cobra.Command{
		Use:           "clock",
		Short:         "Track personal work in Jira Cloud",
		SilenceErrors: true,
		SilenceUsage:  true,
		Version:       fmt.Sprintf("%s (revision %s)", version, revision),
		Long: `clock is a personal Jira Cloud time-tracking CLI.

Configuration:
  clock configure

Configuration validates the Jira Cloud site and authenticated identity before
atomically saving non-secret settings. The API token is stored only in the
secure native credential store.`,
	}
	root.SetVersionTemplate("clock {{.Version}}\n")
	root.SetIn(options.In)
	root.SetOut(options.Out)
	root.SetErr(options.Err)
	root.AddCommand(newConfigureCommand(options.Configure, options.Prompter))
	return root
}

func Execute(root *cobra.Command) int {
	if err := root.Execute(); err != nil {
		_, _ = fmt.Fprintf(root.ErrOrStderr(), "Error: %v\n", err)
		return 1
	}
	return 0
}

func newConfigureCommand(runner ConfigureRunner, prompter Prompter) *cobra.Command {
	return &cobra.Command{
		Use:   "configure",
		Short: "Validate and securely store one Jira Cloud identity",
		Long: `Interactively prompt for a Jira Cloud site, Atlassian email, exact CZK
Hourly rate, and a hidden API token. Leaving the token blank on a rerun retains
the credential for the unchanged site and email.

The discovered Jira site and /myself identity must be confirmed before clock
stores the token in the native credential store and atomically saves config.toml.`,
		Args: cobra.NoArgs,
		RunE: func(command *cobra.Command, _ []string) error {
			if runner == nil || prompter == nil {
				return fmt.Errorf("configure command is unavailable")
			}
			siteURL, err := prompter.ReadLine("Jira site URL")
			if err != nil {
				return err
			}
			email, err := prompter.ReadLine("Atlassian email")
			if err != nil {
				return err
			}
			rateText, err := prompter.ReadLine("Hourly rate (CZK)")
			if err != nil {
				return err
			}
			rate, err := earnings.ParseHourlyRate(rateText)
			if err != nil {
				return err
			}
			tokenText, err := prompter.ReadSecret("API token (leave blank to retain existing)")
			if err != nil {
				return err
			}
			token, _ := secret.NewToken(tokenText)

			result, err := runner.Run(command.Context(), appconfigure.Input{
				SiteURL: siteURL, Email: email, HourlyRate: rate, Token: token,
			})
			if err != nil {
				return err
			}
			return renderConfigured(command.OutOrStdout(), result)
		},
	}
}

func renderConfigured(writer io.Writer, result appconfigure.Result) error {
	_, err := fmt.Fprintf(
		writer,
		"Configured Jira identity\nSite: %s\nName: %s\nEmail: %s\nAccount ID: %s\nHourly rate: %s CZK\n",
		result.Identity.SiteURL,
		result.Identity.DisplayName,
		result.Identity.Email,
		result.Identity.AccountID,
		result.HourlyRate.QuotedCZK(),
	)
	return err
}
