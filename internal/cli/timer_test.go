package cli_test

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/JirakLu/clock/internal/app/recording"
	apptimer "github.com/JirakLu/clock/internal/app/timer"
	"github.com/JirakLu/clock/internal/cli"
	"github.com/JirakLu/clock/internal/runningtimer"
	"github.com/JirakLu/clock/internal/worklog"
)

func TestFreshRootRunningTimerLifecycleGrammarAndOutput(t *testing.T) {
	t.Parallel()
	location := time.FixedZone("CEST", 7200)
	now := time.Date(2026, 7, 31, 12, 0, 0, 0, location)
	active := runningtimer.Timer{Issue: "CLOCK-14", StartedAt: now.Add(-time.Hour), Description: "Lifecycle", CloudID: "cloud", AccountID: "account"}
	tests := []struct {
		name   string
		args   []string
		setup  func(*fakeTimerRunner)
		assert func(*testing.T, *fakeTimerRunner, string, string)
	}{
		{
			name: "start after last", args: []string{"start", "clock-14", "--after-last", "-d", "Lifecycle"},
			setup: func(f *fakeTimerRunner) { f.startResult = apptimer.StartResult{Timer: active} },
			assert: func(t *testing.T, f *fakeTimerRunner, out, _ string) {
				if f.startInput.Issue != "CLOCK-14" || f.startInput.Mode != recording.AfterLast || f.startInput.Description != "Lifecycle" || !strings.Contains(out, "Started Running timer") {
					t.Errorf("input/output = %#v / %q", f.startInput, out)
				}
			},
		},
		{
			name: "status none", args: []string{"status"},
			assert: func(t *testing.T, _ *fakeTimerRunner, out, _ string) {
				if out != "No Running timer.\n" {
					t.Errorf("stdout = %q", out)
				}
			},
		},
		{
			name: "stop explicit empty description", args: []string{"stop", "--at", "11:30", "--description", ""},
			setup: func(f *fakeTimerRunner) {
				interval, _ := worklog.NewInterval(now.Add(-time.Hour), now.Add(-30*time.Minute))
				f.stopResult = recording.Result{Status: recording.Submitted, Worklog: worklog.Worklog{Issue: "CLOCK-14", Interval: interval}}
			},
			assert: func(t *testing.T, f *fakeTimerRunner, out, _ string) {
				if !f.stopInput.DescriptionOverride || f.stopInput.Description != "" || !f.stopInput.StopAt.Equal(time.Date(2026, 7, 31, 11, 30, 0, 0, location)) || !strings.Contains(out, "Created Worklog") {
					t.Errorf("input/output = %#v / %q", f.stopInput, out)
				}
			},
		},
		{
			name: "discard", args: []string{"discard"},
			setup: func(f *fakeTimerRunner) { f.discardResult = apptimer.DiscardResult{Timer: active} },
			assert: func(t *testing.T, _ *fakeTimerRunner, out, _ string) {
				if !strings.Contains(out, "Discarded Running timer") || !strings.Contains(out, "No Jira Worklog was created") {
					t.Errorf("stdout = %q", out)
				}
			},
		},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			runner := &fakeTimerRunner{}
			if test.setup != nil {
				test.setup(runner)
			}
			var stdout, stderr bytes.Buffer
			root := cli.NewRoot(cli.RootOptions{Timer: runner, Out: &stdout, Err: &stderr, Now: func() time.Time { return now }, Location: location})
			root.SetArgs(test.args)
			if exit := cli.Execute(root); exit != 0 {
				t.Fatalf("exit = %d, stderr = %q", exit, stderr.String())
			}
			test.assert(t, runner, stdout.String(), stderr.String())
		})
	}
}

func TestFreshRootSecondStartShowsActiveTimerAndNextActions(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 7, 31, 12, 0, 0, 0, time.UTC)
	active := runningtimer.Timer{Issue: "CLOCK-14", StartedAt: now.Add(-time.Hour)}
	runner := &fakeTimerRunner{startErr: &apptimer.AlreadyRunningError{Timer: active}}
	var stdout, stderr bytes.Buffer
	root := cli.NewRoot(cli.RootOptions{Timer: runner, Out: &stdout, Err: &stderr, Now: func() time.Time { return now }})
	root.SetArgs([]string{"start", "OTHER-1"})
	if exit := cli.Execute(root); exit == 0 {
		t.Fatal("second start succeeded")
	}
	for _, want := range []string{"CLOCK-14", "1h", "clock stop", "clock discard"} {
		if !strings.Contains(stderr.String(), want) {
			t.Errorf("stderr = %q, want %q", stderr.String(), want)
		}
	}
}

func TestFreshRootStatusDisplaysActiveTimerAndIdentityWarning(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 7, 31, 12, 0, 0, 0, time.UTC)
	active := runningtimer.Timer{Issue: "CLOCK-14", StartedAt: now.Add(-time.Hour), Description: "Lifecycle"}
	runner := &fakeTimerRunner{statusResult: apptimer.StatusResult{Active: true, Timer: active, ElapsedSeconds: 3600, IdentityMismatch: true}}
	var stdout, stderr bytes.Buffer
	root := cli.NewRoot(cli.RootOptions{Timer: runner, Out: &stdout, Err: &stderr, Now: func() time.Time { return now }})
	root.SetArgs([]string{"status"})
	if exit := cli.Execute(root); exit != 0 {
		t.Fatalf("status exit = %d, stderr = %q", exit, stderr.String())
	}
	for _, want := range []string{"CLOCK-14", "1h", "Lifecycle"} {
		if !strings.Contains(stdout.String(), want) {
			t.Errorf("stdout = %q, want %q", stdout.String(), want)
		}
	}
	if !strings.Contains(stderr.String(), "different configured Jira identity") {
		t.Errorf("stderr = %q", stderr.String())
	}
}

func TestFreshRootStopRendersPostConsumptionRecoveryFacts(t *testing.T) {
	t.Parallel()
	start := time.Date(2026, 7, 31, 11, 0, 0, 0, time.UTC)
	interval, _ := worklog.NewInterval(start, start.Add(time.Hour))
	runner := &fakeTimerRunner{stopResult: recording.Result{Status: recording.Uncertain, Attempt: worklog.Draft{Issue: "CLOCK-14", Interval: interval, Description: "Recovery"}, Cause: errors.New("connection reset")}}
	var stdout, stderr bytes.Buffer
	root := cli.NewRoot(cli.RootOptions{Timer: runner, Out: &stdout, Err: &stderr})
	root.SetArgs([]string{"stop"})
	if exit := cli.Execute(root); exit == 0 {
		t.Fatal("uncertain stop succeeded")
	}
	for _, want := range []string{"Inspect Jira before retrying", "CLOCK-14", "1h", "Recovery"} {
		if !strings.Contains(stderr.String(), want) {
			t.Errorf("stderr = %q, want %q", stderr.String(), want)
		}
	}
	if stdout.Len() != 0 {
		t.Errorf("stdout = %q", stdout.String())
	}
}

func TestFreshRootStopAndDiscardWithoutTimerUseStableDiagnostic(t *testing.T) {
	t.Parallel()
	for _, command := range []string{"stop", "discard"} {
		command := command
		t.Run(command, func(t *testing.T) {
			t.Parallel()
			runner := &fakeTimerRunner{stopErr: runningtimer.ErrNoTimer, discardErr: runningtimer.ErrNoTimer}
			var stdout, stderr bytes.Buffer
			root := cli.NewRoot(cli.RootOptions{Timer: runner, Out: &stdout, Err: &stderr})
			root.SetArgs([]string{command})
			if exit := cli.Execute(root); exit == 0 {
				t.Fatalf("%s unexpectedly succeeded", command)
			}
			if stdout.Len() != 0 || stderr.String() != "Error: No Running timer.\n" {
				t.Errorf("stdout/stderr = %q / %q", stdout.String(), stderr.String())
			}
		})
	}
}

type fakeTimerRunner struct {
	startInput    apptimer.StartInput
	startResult   apptimer.StartResult
	startErr      error
	statusResult  apptimer.StatusResult
	statusErr     error
	stopInput     apptimer.StopInput
	stopResult    apptimer.StopResult
	stopErr       error
	discardResult apptimer.DiscardResult
	discardErr    error
}

func (f *fakeTimerRunner) Start(_ context.Context, input apptimer.StartInput) (apptimer.StartResult, error) {
	f.startInput = input
	return f.startResult, f.startErr
}
func (f *fakeTimerRunner) Status() (apptimer.StatusResult, error) { return f.statusResult, f.statusErr }
func (f *fakeTimerRunner) Stop(_ context.Context, input apptimer.StopInput) (apptimer.StopResult, error) {
	f.stopInput = input
	return f.stopResult, f.stopErr
}
func (f *fakeTimerRunner) Discard() (apptimer.DiscardResult, error) {
	return f.discardResult, f.discardErr
}
