package cli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strings"
	"time"

	appconfigure "github.com/JirakLu/clock/internal/app/configure"
	applog "github.com/JirakLu/clock/internal/app/log"
	"github.com/JirakLu/clock/internal/app/recording"
	appreport "github.com/JirakLu/clock/internal/app/report"
	"github.com/JirakLu/clock/internal/earnings"
	"github.com/JirakLu/clock/internal/secret"
	"github.com/JirakLu/clock/internal/worklog"
	"github.com/spf13/cobra"
)

type ConfigureRunner interface {
	Run(context.Context, appconfigure.Input) (appconfigure.Result, error)
}

type LogRunner interface {
	Run(context.Context, applog.Input) (applog.Result, error)
}

type ReportRunner interface {
	Run(context.Context, appreport.Input) (appreport.Result, error)
}

type Prompter interface {
	ReadLine(string) (string, error)
	ReadSecret(string) (string, error)
}

type RootOptions struct {
	Configure ConfigureRunner
	Log       LogRunner
	Report    ReportRunner
	Prompter  Prompter
	In        io.Reader
	Out       io.Writer
	Err       io.Writer
	Version   string
	Revision  string
	Now       func() time.Time
	Location  *time.Location
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

Completed Worklogs:
  clock log <issue> <duration> [--at <start>] [-d|--description <text>]
  clock log <issue> --after-last [-d|--description <text>]

Reports:
  clock report today [--earnings] [--json]
  clock report last-week [--earnings] [--json]
  clock report last-month [--earnings] [--json]
  clock report --from <bound> --to <bound> [--earnings] [--json]

Configuration validates the Jira Cloud site and authenticated identity before
atomically saving non-secret settings. The API token is stored only in the
secure native credential store.

Manual Duration accepts positive compact hours and minutes (30m, 2h, 2h30m).
--at accepts today's HH:MM, local YYYY-MM-DDTHH:MM, or an offset-bearing
YYYY-MM-DDTHH:MM+02:00. --after-last conflicts with Duration and --at.`,
	}
	root.SetVersionTemplate("clock {{.Version}}\n")
	root.SetIn(options.In)
	root.SetOut(options.Out)
	root.SetErr(options.Err)
	root.AddCommand(newConfigureCommand(options.Configure, options.Prompter))
	root.AddCommand(newLogCommand(options))
	root.AddCommand(newReportCommand(options))
	return root
}

func newLogCommand(options RootOptions) *cobra.Command {
	var (
		at          string
		afterLast   bool
		description string
	)
	command := &cobra.Command{
		Use:   "log <issue> [duration]",
		Short: "Create one completed Jira Worklog",
		Long: `Create one completed Jira Worklog.

With a Duration, the Worklog ends now unless --at supplies its start.
With --after-last, it starts after today's latest accessible authored Worklog
and ends now. --after-last cannot be combined with a Duration or --at.

Duration uses positive compact hours and minutes: 30m, 2h, or 2h30m.
--at accepts today's HH:MM, local YYYY-MM-DDTHH:MM, or an offset-bearing
YYYY-MM-DDTHH:MM+02:00. Ambiguous local daylight-saving times require an offset.`,
		Args: cobra.RangeArgs(1, 2),
		RunE: func(command *cobra.Command, args []string) error {
			if options.Log == nil {
				return errors.New("log command is unavailable")
			}
			issue, err := worklog.ParseIssueKey(args[0])
			if err != nil {
				return err
			}
			atSet := command.Flags().Changed("at")
			if afterLast && atSet {
				return errors.New("--after-last and --at cannot be used together")
			}

			timing := recording.Timing{}
			if afterLast {
				if len(args) == 2 {
					return errors.New("--after-last does not accept a Duration")
				}
				timing.Mode = recording.AfterLast
			} else {
				if len(args) != 2 {
					return errors.New("clock log requires a Duration unless --after-last is used")
				}
				duration, err := worklog.ParseCompactDuration(args[1])
				if err != nil {
					return err
				}
				timing = recording.Timing{Mode: recording.EndingNow, Duration: duration}
				if atSet {
					now := time.Now()
					if options.Now != nil {
						now = options.Now()
					}
					location := options.Location
					if location == nil {
						location = time.Local
					}
					start, err := parseMinuteTimestamp(at, now, location)
					if err != nil {
						return err
					}
					timing.Mode = recording.AtStart
					timing.Start = start
				}
			}

			result, err := options.Log.Run(command.Context(), applog.Input{
				Issue: issue, Timing: timing, Description: description,
			})
			if err != nil {
				return err
			}
			switch result.Status {
			case recording.Submitted:
				return renderCreatedWorklog(command.OutOrStdout(), result.Worklog)
			case recording.Rejected, recording.Uncertain:
				return renderWorklogFailure(result)
			default:
				return errors.New("log command returned an invalid result")
			}
		},
	}
	command.Flags().StringVar(&at, "at", "", "start at HH:MM or a minute-precise timestamp")
	command.Flags().BoolVar(&afterLast, "after-last", false, "start after today's latest authored Worklog")
	command.Flags().StringVarP(&description, "description", "d", "", "optional Worklog description")
	return command
}

func renderCreatedWorklog(writer io.Writer, created worklog.Worklog) error {
	duration, err := worklog.DurationFromSeconds(created.Interval.Seconds())
	if err != nil {
		return err
	}
	if _, err := fmt.Fprintf(
		writer,
		"Created Worklog\nIssue: %s\nStart: %s\nEnd: %s\nDuration: %s\n",
		created.Issue,
		created.Interval.Start().Format(time.RFC3339),
		created.Interval.End().Format(time.RFC3339),
		duration,
	); err != nil {
		return err
	}
	if created.Description != "" {
		_, err = fmt.Fprintf(writer, "Description: %s\n", created.Description)
	}
	return err
}

func renderWorklogFailure(result recording.Result) error {
	duration, durationErr := worklog.DurationFromSeconds(result.Attempt.Interval.Seconds())
	if durationErr != nil {
		return durationErr
	}
	description := result.Attempt.Description
	if description == "" {
		description = "(none)"
	}
	classification := "Jira rejected Worklog creation"
	guidance := "Use these facts to recover manually."
	if result.Status == recording.Uncertain {
		classification = "Jira Worklog creation had an uncertain outcome"
		guidance = "Inspect Jira before retrying to avoid creating a duplicate Worklog."
	}
	cause := "unknown Jira failure"
	if result.Cause != nil {
		cause = result.Cause.Error()
	}
	return fmt.Errorf(
		"%s: %s\nManual recovery facts:\nIssue: %s\nStart: %s\nEnd: %s\nDuration: %s\nDescription: %s\n%s",
		classification,
		strings.TrimSpace(cause),
		result.Attempt.Issue,
		result.Attempt.Interval.Start().Format(time.RFC3339),
		result.Attempt.Interval.End().Format(time.RFC3339),
		duration,
		description,
		guidance,
	)
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
