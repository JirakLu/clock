package cli_test

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	appconfigure "github.com/JirakLu/clock/internal/app/configure"
	applog "github.com/JirakLu/clock/internal/app/log"
	"github.com/JirakLu/clock/internal/app/recording"
	"github.com/JirakLu/clock/internal/cli"
	"github.com/JirakLu/clock/internal/jiraidentity"
	"github.com/JirakLu/clock/internal/worklog"
)

func TestFreshRootConfigureUsesTypedInputAndSeparatesOutput(t *testing.T) {
	t.Parallel()

	const tokenRaw = "never-echo-this-token"
	runner := &fakeConfigureRunner{
		result: appconfigure.Result{
			Identity: jiraidentity.Identity{
				Reference: jiraidentity.Reference{
					SiteURL: "https://example.atlassian.net", CloudID: "cloud-123",
					Email: "person@example.com", AccountID: "account-456",
				},
				DisplayName: "Example Person",
			},
		},
	}
	prompter := &fakePrompter{
		lines:  []string{"https://EXAMPLE.atlassian.net/", "person@example.com", "750.00"},
		secret: tokenRaw,
	}
	var stdout, stderr bytes.Buffer
	root := cli.NewRoot(cli.RootOptions{
		Configure: runner, Prompter: prompter,
		In: strings.NewReader(""), Out: &stdout, Err: &stderr,
		Version: "v1.2.3", Revision: "abc123",
	})
	root.SetArgs([]string{"configure"})

	if exitCode := cli.Execute(root); exitCode != 0 {
		t.Fatalf("Execute() exit code = %d, stderr = %s", exitCode, stderr.String())
	}
	if runner.input.SiteURL != "https://EXAMPLE.atlassian.net/" ||
		runner.input.Email != "person@example.com" ||
		runner.input.HourlyRate.QuotedCZK() != "750.00" ||
		runner.input.Token.Value() != tokenRaw {
		t.Errorf("configure input = %#v", runner.input)
	}
	if !strings.Contains(stdout.String(), "Configured Jira identity") ||
		!strings.Contains(stdout.String(), "Example Person") ||
		!strings.Contains(stdout.String(), "https://example.atlassian.net") {
		t.Errorf("stdout = %q, want structured configuration result", stdout.String())
	}
	if strings.Contains(stdout.String(), tokenRaw) || strings.Contains(stderr.String(), tokenRaw) {
		t.Fatal("API token appeared in command output")
	}
	if stderr.Len() != 0 {
		t.Errorf("stderr = %q on success", stderr.String())
	}
}

func TestFreshRootLogAcceptsEveryCommandForm(t *testing.T) {
	t.Parallel()

	location := time.FixedZone("CEST", 2*60*60)
	now := time.Date(2026, time.July, 27, 12, 34, 56, 0, location)
	tests := []struct {
		name        string
		args        []string
		wantMode    recording.TimingMode
		wantStart   time.Time
		wantSeconds int64
		wantDesc    string
	}{
		{
			name: "duration ending now", args: []string{"log", "clock-13", "30m"},
			wantMode: recording.EndingNow, wantSeconds: 1800,
		},
		{
			name:        "duration at today's time",
			args:        []string{"log", "CLOCK-13", "2h30m", "--at", "09:15", "-d", "Planning"},
			wantMode:    recording.AtStart,
			wantStart:   time.Date(2026, time.July, 27, 9, 15, 0, 0, location),
			wantSeconds: 9000, wantDesc: "Planning",
		},
		{
			name:        "duration at offset timestamp",
			args:        []string{"log", "CLOCK-13", "1h", "--at", "2026-07-26T20:00+01:00"},
			wantMode:    recording.AtStart,
			wantStart:   time.Date(2026, time.July, 26, 20, 0, 0, 0, time.FixedZone("", 60*60)),
			wantSeconds: 3600,
		},
		{
			name:     "after latest",
			args:     []string{"log", "CLOCK-13", "--after-last", "--description", "Follow-up"},
			wantMode: recording.AfterLast, wantDesc: "Follow-up",
		},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			runner := &fakeLogRunner{result: submittedLogResult(now)}
			var stdout, stderr bytes.Buffer
			root := cli.NewRoot(cli.RootOptions{
				Log: runner, In: strings.NewReader(""), Out: &stdout, Err: &stderr,
				Now: func() time.Time { return now }, Location: location,
			})
			root.SetArgs(test.args)

			if exitCode := cli.Execute(root); exitCode != 0 {
				t.Fatalf("Execute() exit code = %d, stderr = %s", exitCode, stderr.String())
			}
			if runner.calls != 1 || runner.input.Issue.String() != "CLOCK-13" ||
				runner.input.Timing.Mode != test.wantMode ||
				runner.input.Timing.Duration.Seconds() != test.wantSeconds ||
				runner.input.Description != test.wantDesc {
				t.Errorf("log input = %#v", runner.input)
			}
			if !test.wantStart.IsZero() && !runner.input.Timing.Start.Equal(test.wantStart) {
				t.Errorf("start = %v, want %v", runner.input.Timing.Start, test.wantStart)
			}
			if !strings.Contains(stdout.String(), "Created Worklog") ||
				!strings.Contains(stdout.String(), "CLOCK-13") ||
				stderr.Len() != 0 {
				t.Errorf("stdout = %q, stderr = %q", stdout.String(), stderr.String())
			}
		})
	}
}

func TestFreshRootLogRejectsInvalidGrammarBeforeApplication(t *testing.T) {
	t.Parallel()

	prague, err := time.LoadLocation("Europe/Prague")
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, time.October, 25, 12, 0, 0, 0, prague)
	tests := []struct {
		name string
		args []string
		want string
	}{
		{name: "URL issue", args: []string{"log", "https://example.atlassian.net/browse/CLOCK-13", "1h"}, want: "Jira issue key"},
		{name: "bare issue number", args: []string{"log", "13", "1h"}, want: "Jira issue key"},
		{name: "zero Duration", args: []string{"log", "CLOCK-13", "0m"}, want: "positive"},
		{name: "seconds Duration", args: []string{"log", "CLOCK-13", "30s"}, want: "compact hours and minutes"},
		{name: "missing Duration", args: []string{"log", "CLOCK-13"}, want: "requires a Duration"},
		{name: "Duration conflicts after-last", args: []string{"log", "CLOCK-13", "1h", "--after-last"}, want: "does not accept a Duration"},
		{name: "at conflicts after-last", args: []string{"log", "CLOCK-13", "--after-last", "--at", "09:00"}, want: "cannot be used together"},
		{name: "timestamp seconds", args: []string{"log", "CLOCK-13", "1h", "--at", "2026-10-25T02:30:00"}, want: "minute-precise"},
		{name: "ambiguous local timestamp", args: []string{"log", "CLOCK-13", "1h", "--at", "2026-10-25T02:30"}, want: "ambiguous"},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			runner := &fakeLogRunner{}
			var stdout, stderr bytes.Buffer
			root := cli.NewRoot(cli.RootOptions{
				Log: runner, In: strings.NewReader(""), Out: &stdout, Err: &stderr,
				Now: func() time.Time { return now }, Location: prague,
			})
			root.SetArgs(test.args)
			if exitCode := cli.Execute(root); exitCode == 0 {
				t.Fatal("Execute() unexpectedly succeeded")
			}
			if runner.calls != 0 {
				t.Errorf("Log.Run() called %d times", runner.calls)
			}
			if stdout.Len() != 0 || !strings.Contains(stderr.String(), test.want) {
				t.Errorf("stdout = %q, stderr = %q, want %q", stdout.String(), stderr.String(), test.want)
			}
		})
	}
}

func TestFreshRootLogRendersTypedJiraRecoveryFacts(t *testing.T) {
	t.Parallel()

	start := time.Date(2026, time.July, 27, 9, 0, 0, 0, time.UTC)
	interval, _ := worklog.NewInterval(start, start.Add(time.Hour))
	for _, test := range []struct {
		name   string
		status recording.Status
		want   string
	}{
		{name: "definite rejection", status: recording.Rejected, want: "Jira rejected"},
		{name: "uncertain outcome", status: recording.Uncertain, want: "Inspect Jira before retrying"},
	} {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			runner := &fakeLogRunner{result: recording.Result{
				Status: test.status,
				Attempt: worklog.Draft{
					Issue: "CLOCK-13", Interval: interval, Description: "Recovery",
				},
				Cause: errors.New("Jira failure"),
			}}
			var stdout, stderr bytes.Buffer
			root := cli.NewRoot(cli.RootOptions{
				Log: runner, In: strings.NewReader(""), Out: &stdout, Err: &stderr,
			})
			root.SetArgs([]string{"log", "CLOCK-13", "1h"})
			if exitCode := cli.Execute(root); exitCode == 0 {
				t.Fatal("Execute() unexpectedly succeeded")
			}
			for _, want := range []string{
				test.want, "CLOCK-13", start.Format(time.RFC3339),
				start.Add(time.Hour).Format(time.RFC3339), "1h", "Recovery",
			} {
				if !strings.Contains(stderr.String(), want) {
					t.Errorf("stderr = %q, want %q", stderr.String(), want)
				}
			}
			if stdout.Len() != 0 {
				t.Errorf("stdout = %q on failure", stdout.String())
			}
		})
	}
}

func TestFreshRootFailureWritesDiagnosticOnlyToStderr(t *testing.T) {
	t.Parallel()

	var stdout, stderr bytes.Buffer
	root := cli.NewRoot(cli.RootOptions{
		Configure: &fakeConfigureRunner{err: errors.New("Jira rejected the credentials")},
		Prompter: &fakePrompter{
			lines:  []string{"https://example.atlassian.net", "person@example.com", "750"},
			secret: "secret",
		},
		In: strings.NewReader(""), Out: &stdout, Err: &stderr,
	})
	root.SetArgs([]string{"configure"})

	if exitCode := cli.Execute(root); exitCode == 0 {
		t.Fatal("Execute() unexpectedly succeeded")
	}
	if stdout.Len() != 0 {
		t.Errorf("stdout = %q on failure", stdout.String())
	}
	if !strings.Contains(stderr.String(), "Jira rejected the credentials") {
		t.Errorf("stderr = %q, want diagnostic", stderr.String())
	}
}

func TestFreshRootHelpAndVersionExposeShippedContract(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		args []string
		want []string
	}{
		{
			name: "help", args: []string{"--help"},
			want: []string{
				"clock configure", "clock log <issue> <duration>",
				"secure native credential store", "--version",
			},
		},
		{
			name: "version", args: []string{"--version"},
			want: []string{"clock v1.2.3 (revision abc123)"},
		},
	}

	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			var stdout, stderr bytes.Buffer
			root := cli.NewRoot(cli.RootOptions{
				Configure: &fakeConfigureRunner{}, Prompter: &fakePrompter{},
				In: strings.NewReader(""), Out: &stdout, Err: &stderr,
				Version: "v1.2.3", Revision: "abc123",
			})
			root.SetArgs(test.args)

			if exitCode := cli.Execute(root); exitCode != 0 {
				t.Fatalf("Execute() exit code = %d, stderr = %s", exitCode, stderr.String())
			}
			for _, expected := range test.want {
				if !strings.Contains(stdout.String(), expected) {
					t.Errorf("stdout = %q, want %q", stdout.String(), expected)
				}
			}
			if strings.Contains(strings.ToLower(stdout.String()), "build time") {
				t.Errorf("output contains build timestamp: %q", stdout.String())
			}
		})
	}
}

type fakeConfigureRunner struct {
	input  appconfigure.Input
	result appconfigure.Result
	err    error
}

func (f *fakeConfigureRunner) Run(_ context.Context, input appconfigure.Input) (appconfigure.Result, error) {
	f.input = input
	return f.result, f.err
}

type fakePrompter struct {
	lines  []string
	secret string
}

type fakeLogRunner struct {
	input  applog.Input
	result applog.Result
	err    error
	calls  int
}

func (f *fakeLogRunner) Run(_ context.Context, input applog.Input) (applog.Result, error) {
	f.calls++
	f.input = input
	return f.result, f.err
}

func submittedLogResult(now time.Time) recording.Result {
	interval, _ := worklog.NewInterval(now.Add(-30*time.Minute), now)
	return recording.Result{
		Status: recording.Submitted,
		Worklog: worklog.Worklog{
			ID: "created", AuthorID: "account",
			Issue: "CLOCK-13", Interval: interval, Description: "Done",
		},
	}
}

func (f *fakePrompter) ReadLine(string) (string, error) {
	if len(f.lines) == 0 {
		return "", errors.New("unexpected prompt")
	}
	line := f.lines[0]
	f.lines = f.lines[1:]
	return line, nil
}

func (f *fakePrompter) ReadSecret(string) (string, error) {
	return f.secret, nil
}
