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
	apptimer "github.com/JirakLu/clock/internal/app/timer"
	"github.com/JirakLu/clock/internal/earnings"
	"github.com/JirakLu/clock/internal/runningtimer"
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

type TimerRunner interface {
	Start(context.Context, apptimer.StartInput) (apptimer.StartResult, error)
	Status() (apptimer.StatusResult, error)
	Stop(context.Context, apptimer.StopInput) (apptimer.StopResult, error)
	Discard(apptimer.DiscardInput) (apptimer.DiscardResult, error)
}

type Prompter interface {
	ReadLine(string) (string, error)
	ReadSecret(string) (string, error)
}

type RootOptions struct {
	Configure ConfigureRunner
	Log       LogRunner
	Report    ReportRunner
	Timer     TimerRunner
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

Running timer:
  clock start <issue> [--at <start> | --after-last] [-d|--description <text>]
  clock status
  clock stop [--at <stop>] [-d|--description <text>]
  clock discard [--force]

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
	root.AddCommand(newStartCommand(options))
	root.AddCommand(newStatusCommand(options))
	root.AddCommand(newStopCommand(options))
	root.AddCommand(newDiscardCommand(options))
	return root
}

func newStartCommand(options RootOptions) *cobra.Command {
	var at, description string
	var afterLast bool
	command := &cobra.Command{
		Use: "start <issue>", Short: "Start one local Running timer", Args: cobra.ExactArgs(1),
		RunE: func(command *cobra.Command, args []string) error {
			if options.Timer == nil {
				return errors.New("start command is unavailable")
			}
			issue, err := worklog.ParseIssueKey(args[0])
			if err != nil {
				return err
			}
			if afterLast && command.Flags().Changed("at") {
				return errors.New("--after-last and --at cannot be used together")
			}
			mode := recording.EndingNow
			var start time.Time
			if afterLast {
				mode = recording.AfterLast
			} else if command.Flags().Changed("at") {
				mode = recording.AtStart
				now, location := optionTime(options)
				start, err = parseMinuteTimestamp(at, now, location)
				if err != nil {
					return err
				}
			}
			result, err := options.Timer.Start(command.Context(), apptimer.StartInput{Issue: issue, Mode: mode, ExplicitStart: start, Description: description})
			if err != nil {
				var active *apptimer.AlreadyRunningError
				if errors.As(err, &active) {
					renderWarnings(command.ErrOrStderr(), active.Warnings)
					return fmt.Errorf("%w\n%s\nUse clock stop to create a Worklog or clock discard to abandon it", err, renderTimerFacts(active.Timer, optionsNow(options)))
				}
				return err
			}
			_, err = fmt.Fprintf(command.OutOrStdout(), "Started Running timer\n%s\n", renderTimerFacts(result.Timer, optionsNow(options)))
			return err
		},
	}
	command.Flags().StringVar(&at, "at", "", "start at HH:MM or a minute-precise timestamp")
	command.Flags().BoolVar(&afterLast, "after-last", false, "start after today's latest authored Worklog")
	command.Flags().StringVarP(&description, "description", "d", "", "optional Running timer description")
	return command
}

func newStatusCommand(options RootOptions) *cobra.Command {
	return &cobra.Command{Use: "status", Short: "Inspect the Running timer", Args: cobra.NoArgs,
		RunE: func(command *cobra.Command, _ []string) error {
			if options.Timer == nil {
				return errors.New("status command is unavailable")
			}
			result, err := options.Timer.Status()
			if err != nil {
				return err
			}
			if !result.Active {
				_, err = fmt.Fprintln(command.OutOrStdout(), "No Running timer.")
				return err
			}
			renderWarnings(command.ErrOrStderr(), result.Warnings)
			if result.IdentityMismatch {
				_, _ = fmt.Fprintln(command.ErrOrStderr(), "Warning: Running timer belongs to a different configured Jira identity; clock stop will refuse submission.")
			}
			_, err = fmt.Fprintf(command.OutOrStdout(), "Running timer\nIssue: %s\nStart: %s\nElapsed: %s\n", result.Timer.Issue, result.Timer.StartedAt.Format(time.RFC3339Nano), timerFormatSeconds(result.ElapsedSeconds))
			if err == nil && result.Timer.Description != "" {
				_, err = fmt.Fprintf(command.OutOrStdout(), "Description: %s\n", result.Timer.Description)
			}
			return err
		}}
}

func newStopCommand(options RootOptions) *cobra.Command {
	var at, description string
	command := &cobra.Command{Use: "stop", Short: "Stop the Running timer into one Jira Worklog", Args: cobra.NoArgs,
		RunE: func(command *cobra.Command, _ []string) error {
			if options.Timer == nil {
				return errors.New("stop command is unavailable")
			}
			var stopAt time.Time
			var err error
			if command.Flags().Changed("at") {
				now, location := optionTime(options)
				stopAt, err = parseMinuteTimestamp(at, now, location)
				if err != nil {
					return err
				}
			}
			result, err := options.Timer.Stop(command.Context(), apptimer.StopInput{StopAt: stopAt, Description: description, DescriptionOverride: command.Flags().Changed("description")})
			renderWarnings(command.ErrOrStderr(), result.Warnings)
			if err != nil {
				return err
			}
			switch result.Status {
			case recording.Submitted:
				return renderCreatedWorklog(command.OutOrStdout(), result.Worklog)
			case recording.Rejected, recording.Uncertain:
				return renderWorklogFailure(result.Result)
			default:
				return errors.New("stop command returned an invalid result")
			}
		}}
	command.Flags().StringVar(&at, "at", "", "stop at a past HH:MM or minute-precise timestamp")
	command.Flags().StringVarP(&description, "description", "d", "", "replace the stored description, including with empty text")
	return command
}

func newDiscardCommand(options RootOptions) *cobra.Command {
	var force bool
	command := &cobra.Command{Use: "discard", Short: "Discard Running timer state without contacting Jira", Args: cobra.NoArgs,
		RunE: func(command *cobra.Command, _ []string) error {
			if options.Timer == nil {
				return errors.New("discard command is unavailable")
			}
			result, err := options.Timer.Discard(apptimer.DiscardInput{Force: force})
			renderWarnings(command.ErrOrStderr(), result.Warnings)
			if result.Forced {
				for _, path := range result.Removed {
					if _, writeErr := fmt.Fprintf(command.OutOrStdout(), "Removed Running timer artifact: %s\n", path); writeErr != nil {
						return writeErr
					}
				}
				if _, writeErr := fmt.Fprintln(command.OutOrStdout(), "No Jira Worklog was created."); writeErr != nil {
					return writeErr
				}
				if err != nil {
					return err
				}
				_, err = fmt.Fprintln(command.OutOrStdout(), "Forced discard completed.")
				return err
			}
			if err != nil {
				return err
			}
			_, err = fmt.Fprintf(command.OutOrStdout(), "Discarded Running timer\n%s\nNo Jira Worklog was created.\n", renderTimerFacts(result.Timer, optionsNow(options)))
			return err
		}}
	command.Flags().BoolVar(&force, "force", false, "remove invalid canonical and staging Running timer state")
	return command
}

func renderWarnings(writer io.Writer, warnings []string) {
	for _, warning := range warnings {
		_, _ = fmt.Fprintln(writer, warning)
	}
}

func optionTime(options RootOptions) (time.Time, *time.Location) {
	now := optionsNow(options)
	location := options.Location
	if location == nil {
		location = time.Local
	}
	return now, location
}

func optionsNow(options RootOptions) time.Time {
	if options.Now != nil {
		return options.Now()
	}
	return time.Now()
}

func renderTimerFacts(timer runningtimer.Timer, now time.Time) string {
	elapsed := int64(now.Sub(timer.StartedAt) / time.Second)
	if elapsed < 0 {
		elapsed = 0
	}
	text := fmt.Sprintf("Issue: %s\nStart: %s\nElapsed: %s", timer.Issue, timer.StartedAt.Format(time.RFC3339Nano), timerFormatSeconds(elapsed))
	if timer.Description != "" {
		text += "\nDescription: " + timer.Description
	}
	return text
}

func timerFormatSeconds(seconds int64) string {
	if seconds <= 0 {
		return "0s"
	}
	duration, _ := worklog.DurationFromSeconds(seconds)
	return duration.String()
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
