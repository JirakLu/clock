package cli_test

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/JirakLu/clock/internal/app/recording"
	apptimer "github.com/JirakLu/clock/internal/app/timer"
	"github.com/JirakLu/clock/internal/cli"
	"github.com/JirakLu/clock/internal/config"
	"github.com/JirakLu/clock/internal/credential"
	"github.com/JirakLu/clock/internal/jiraidentity"
	domainreport "github.com/JirakLu/clock/internal/report"
	"github.com/JirakLu/clock/internal/runningtimer"
	"github.com/JirakLu/clock/internal/secret"
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
				f.stopResult = apptimer.StopResult{Result: recording.Result{Status: recording.Submitted, Worklog: worklog.Worklog{Issue: "CLOCK-14", Interval: interval}}}
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

func TestFreshRootStatusDisplaysStateRecoveryWarnings(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 7, 31, 12, 0, 0, 0, time.UTC)
	active := runningtimer.Timer{Issue: "CLOCK-14", StartedAt: now.Add(-time.Hour)}
	runner := &fakeTimerRunner{statusResult: apptimer.StatusResult{
		Active: true, Timer: active, ElapsedSeconds: 3600,
		Warnings: []string{`Warning: removed orphan Running timer staging state "/tmp/state.json.staging"; canonical state remains active.`},
	}}
	var stdout, stderr bytes.Buffer
	root := cli.NewRoot(cli.RootOptions{Timer: runner, Out: &stdout, Err: &stderr, Now: func() time.Time { return now }})
	root.SetArgs([]string{"status"})
	if exit := cli.Execute(root); exit != 0 {
		t.Fatalf("status exit = %d, stderr = %q", exit, stderr.String())
	}
	if !strings.Contains(stderr.String(), "removed orphan") || !strings.Contains(stderr.String(), "state.json.staging") {
		t.Errorf("stderr = %q", stderr.String())
	}
}

func TestFreshRootsRecoverRealOrphanStagingAndIdentityMismatchState(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 7, 31, 12, 0, 0, 0, time.UTC)
	store := runningtimer.NewStore(t.TempDir())
	active := runningtimer.Timer{Issue: "CLOCK-15", StartedAt: now.Add(-time.Hour), CloudID: "old-cloud", AccountID: "old-account"}
	if err := store.Create(active, now); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(store.StagingPath(), []byte("orphan"), 0o600); err != nil {
		t.Fatal(err)
	}
	recorder := &countingRecoveryRecorder{}
	service := apptimer.New(
		staticConfiguration{value: config.Configuration{JiraIdentity: jiraidentity.Reference{CloudID: "new-cloud", AccountID: "new-account"}}},
		staticCredentials{}, store, recorder, func() time.Time { return now },
	)

	var statusOut, statusErr bytes.Buffer
	statusRoot := cli.NewRoot(cli.RootOptions{Timer: service, Out: &statusOut, Err: &statusErr, Now: func() time.Time { return now }})
	statusRoot.SetArgs([]string{"status"})
	if exit := cli.Execute(statusRoot); exit != 0 {
		t.Fatalf("status exit = %d, stderr = %q", exit, statusErr.String())
	}
	for _, want := range []string{"removed orphan", store.StagingPath(), "different configured Jira identity"} {
		if !strings.Contains(statusErr.String(), want) {
			t.Errorf("status stderr = %q, want %q", statusErr.String(), want)
		}
	}
	if !strings.Contains(statusOut.String(), "CLOCK-15") {
		t.Errorf("status stdout = %q", statusOut.String())
	}
	if _, err := os.Lstat(store.StagingPath()); !os.IsNotExist(err) {
		t.Errorf("orphan staging artifact still exists: %v", err)
	}

	var stopOut, stopErr bytes.Buffer
	stopRoot := cli.NewRoot(cli.RootOptions{Timer: service, Out: &stopOut, Err: &stopErr, Now: func() time.Time { return now }})
	stopRoot.SetArgs([]string{"stop"})
	if exit := cli.Execute(stopRoot); exit == 0 || !strings.Contains(stopErr.String(), "different Jira") {
		t.Errorf("stop exit/stderr = %d / %q", exit, stopErr.String())
	}
	if recorder.calls != 0 {
		t.Errorf("identity-mismatched stop contacted Jira %d times", recorder.calls)
	}
	if _, err := os.Stat(store.Path()); err != nil {
		t.Errorf("identity-mismatched stop mutated canonical state: %v", err)
	}

	var discardOut, discardErr bytes.Buffer
	discardRoot := cli.NewRoot(cli.RootOptions{Timer: service, Out: &discardOut, Err: &discardErr, Now: func() time.Time { return now }})
	discardRoot.SetArgs([]string{"discard"})
	if exit := cli.Execute(discardRoot); exit != 0 {
		t.Fatalf("discard exit = %d, stderr = %q", exit, discardErr.String())
	}
	if !strings.Contains(discardOut.String(), "No Jira Worklog was created") {
		t.Errorf("discard stdout = %q", discardOut.String())
	}
	if _, err := os.Stat(store.Path()); !os.IsNotExist(err) {
		t.Errorf("ordinary discard did not remove identity-mismatched state: %v", err)
	}
}

func TestFreshRootStopRendersPostConsumptionRecoveryFacts(t *testing.T) {
	t.Parallel()
	start := time.Date(2026, 7, 31, 11, 0, 0, 0, time.UTC)
	interval, _ := worklog.NewInterval(start, start.Add(time.Hour))
	runner := &fakeTimerRunner{stopResult: apptimer.StopResult{Result: recording.Result{Status: recording.Uncertain, Attempt: worklog.Draft{Issue: "CLOCK-14", Interval: interval, Description: "Recovery"}, Cause: errors.New("connection reset")}}}
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

func TestFreshRootForcedDiscardReportsEveryRemovedArtifactAndNoWorklog(t *testing.T) {
	t.Parallel()
	runner := &fakeTimerRunner{discardResult: apptimer.DiscardResult{
		Forced:  true,
		Removed: []string{"/tmp/clock/state.json", "/tmp/clock/state.json.staging"},
	}}
	var stdout, stderr bytes.Buffer
	root := cli.NewRoot(cli.RootOptions{Timer: runner, Out: &stdout, Err: &stderr})
	root.SetArgs([]string{"discard", "--force"})
	if exit := cli.Execute(root); exit != 0 {
		t.Fatalf("discard --force exit = %d, stderr = %q", exit, stderr.String())
	}
	if !runner.discardInput.Force {
		t.Error("discard input did not request forced recovery")
	}
	for _, want := range []string{"/tmp/clock/state.json", "/tmp/clock/state.json.staging", "Forced discard completed", "No Jira Worklog was created"} {
		if !strings.Contains(stdout.String(), want) {
			t.Errorf("stdout = %q, want %q", stdout.String(), want)
		}
	}
}

func TestFreshRootForcedDiscardDoesNotClaimCompleteRecoveryAfterPartialRemoval(t *testing.T) {
	t.Parallel()
	runner := &fakeTimerRunner{
		discardResult: apptimer.DiscardResult{Forced: true, Removed: []string{"/tmp/clock/state.json.staging"}},
		discardErr:    errors.New(`forced discard incomplete: remove Running timer artifact "/tmp/clock/state.json": permission denied`),
	}
	var stdout, stderr bytes.Buffer
	root := cli.NewRoot(cli.RootOptions{Timer: runner, Out: &stdout, Err: &stderr})
	root.SetArgs([]string{"discard", "--force"})
	if exit := cli.Execute(root); exit == 0 {
		t.Fatal("partial forced discard succeeded")
	}
	if !strings.Contains(stdout.String(), "/tmp/clock/state.json.staging") || !strings.Contains(stdout.String(), "No Jira Worklog was created") {
		t.Errorf("stdout = %q", stdout.String())
	}
	if strings.Contains(stdout.String(), "completed") {
		t.Errorf("stdout claims complete recovery: %q", stdout.String())
	}
	if !strings.Contains(stderr.String(), "incomplete") || !strings.Contains(stderr.String(), "/tmp/clock/state.json") {
		t.Errorf("stderr = %q", stderr.String())
	}
}

func TestFreshRootTimerCommandsFailClosedOnInvalidStateWithoutJiraContact(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 7, 31, 12, 0, 0, 0, time.UTC)
	for _, args := range [][]string{{"status"}, {"start", "CLOCK-15"}, {"stop"}, {"discard"}} {
		args := args
		t.Run(args[0], func(t *testing.T) {
			t.Parallel()
			store := runningtimer.NewStore(t.TempDir())
			if err := os.MkdirAll(filepath.Dir(store.Path()), 0o700); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(store.Path(), []byte(`{"schema_version":`), 0o600); err != nil {
				t.Fatal(err)
			}
			recorder := &countingRecoveryRecorder{}
			service := apptimer.New(nil, nil, store, recorder, func() time.Time { return now })
			var stdout, stderr bytes.Buffer
			root := cli.NewRoot(cli.RootOptions{Timer: service, Out: &stdout, Err: &stderr, Now: func() time.Time { return now }})
			root.SetArgs(args)
			if exit := cli.Execute(root); exit == 0 {
				t.Fatalf("%v unexpectedly succeeded", args)
			}
			for _, want := range []string{store.Path(), "parse", "clock discard --force"} {
				if !strings.Contains(stderr.String(), want) {
					t.Errorf("stderr = %q, want %q", stderr.String(), want)
				}
			}
			if stdout.Len() != 0 || recorder.calls != 0 {
				t.Errorf("stdout/Jira calls = %q / %d", stdout.String(), recorder.calls)
			}
			if _, err := os.Stat(store.Path()); err != nil {
				t.Errorf("invalid canonical state was mutated: %v", err)
			}
		})
	}
}

func TestFreshRootForcedDiscardUsesRealStateAndNeverContactsJira(t *testing.T) {
	t.Parallel()
	store := runningtimer.NewStore(t.TempDir())
	if err := os.MkdirAll(filepath.Dir(store.Path()), 0o700); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{store.Path(), store.StagingPath()} {
		if err := os.WriteFile(path, []byte("invalid"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	recorder := &countingRecoveryRecorder{}
	service := apptimer.New(nil, nil, store, recorder, time.Now)
	var stdout, stderr bytes.Buffer
	root := cli.NewRoot(cli.RootOptions{Timer: service, Out: &stdout, Err: &stderr})
	root.SetArgs([]string{"discard", "--force"})
	if exit := cli.Execute(root); exit != 0 {
		t.Fatalf("discard --force exit = %d, stderr = %q", exit, stderr.String())
	}
	for _, path := range []string{store.Path(), store.StagingPath()} {
		if !strings.Contains(stdout.String(), path) {
			t.Errorf("stdout = %q, want %q", stdout.String(), path)
		}
		if _, err := os.Stat(path); !os.IsNotExist(err) {
			t.Errorf("artifact %q still exists: %v", path, err)
		}
	}
	if recorder.calls != 0 || !strings.Contains(stdout.String(), "No Jira Worklog was created") {
		t.Errorf("Jira calls/stdout = %d / %q", recorder.calls, stdout.String())
	}
}

func TestFreshRootForcedDiscardRejectsValidOrAbsentState(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 7, 31, 12, 0, 0, 0, time.UTC)
	for _, test := range []struct {
		name  string
		setup func(*testing.T, *runningtimer.Store)
		want  string
	}{
		{
			name: "valid state",
			setup: func(t *testing.T, store *runningtimer.Store) {
				t.Helper()
				if err := store.Create(runningtimer.Timer{Issue: "CLOCK-15", StartedAt: now.Add(-time.Hour), CloudID: "cloud", AccountID: "account"}, now); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(store.StagingPath(), []byte("orphan"), 0o600); err != nil {
					t.Fatal(err)
				}
			},
			want: "state is valid",
		},
		{name: "absent state", setup: func(*testing.T, *runningtimer.Store) {}, want: "no Running timer state artifacts"},
	} {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			store := runningtimer.NewStore(t.TempDir())
			test.setup(t, store)
			recorder := &countingRecoveryRecorder{}
			service := apptimer.New(nil, nil, store, recorder, func() time.Time { return now })
			var stdout, stderr bytes.Buffer
			root := cli.NewRoot(cli.RootOptions{Timer: service, Out: &stdout, Err: &stderr})
			root.SetArgs([]string{"discard", "--force"})
			if exit := cli.Execute(root); exit == 0 {
				t.Fatal("discard --force unexpectedly succeeded")
			}
			if !strings.Contains(stderr.String(), test.want) || strings.Contains(stdout.String(), "completed") {
				t.Errorf("stdout/stderr = %q / %q", stdout.String(), stderr.String())
			}
			if recorder.calls != 0 {
				t.Errorf("Jira calls = %d", recorder.calls)
			}
			if test.name == "valid state" {
				if _, err := os.Stat(store.Path()); err != nil {
					t.Errorf("valid state was mutated: %v", err)
				}
				if !strings.Contains(stdout.String(), store.StagingPath()) {
					t.Errorf("stdout = %q, want removed staging path", stdout.String())
				}
				if _, err := os.Lstat(store.StagingPath()); !os.IsNotExist(err) {
					t.Errorf("orphan staging artifact still exists: %v", err)
				}
			}
		})
	}
}

func TestFreshRootsDiagnoseAndForceDiscardDanglingStateArtifact(t *testing.T) {
	t.Parallel()
	store := runningtimer.NewStore(t.TempDir())
	if err := os.MkdirAll(filepath.Dir(store.Path()), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("missing-target", store.Path()); err != nil {
		t.Fatal(err)
	}
	recorder := &countingRecoveryRecorder{}
	service := apptimer.New(nil, nil, store, recorder, time.Now)

	var statusOut, statusErr bytes.Buffer
	statusRoot := cli.NewRoot(cli.RootOptions{Timer: service, Out: &statusOut, Err: &statusErr})
	statusRoot.SetArgs([]string{"status"})
	if exit := cli.Execute(statusRoot); exit == 0 {
		t.Fatal("status unexpectedly treated dangling state as absent")
	}
	for _, want := range []string{store.Path(), "regular file", "clock discard --force"} {
		if !strings.Contains(statusErr.String(), want) {
			t.Errorf("status stderr = %q, want %q", statusErr.String(), want)
		}
	}

	var discardOut, discardErr bytes.Buffer
	discardRoot := cli.NewRoot(cli.RootOptions{Timer: service, Out: &discardOut, Err: &discardErr})
	discardRoot.SetArgs([]string{"discard", "--force"})
	if exit := cli.Execute(discardRoot); exit != 0 {
		t.Fatalf("discard --force exit = %d, stderr = %q", exit, discardErr.String())
	}
	if !strings.Contains(discardOut.String(), store.Path()) || recorder.calls != 0 {
		t.Errorf("discard stdout/Jira calls = %q / %d", discardOut.String(), recorder.calls)
	}
	if _, err := os.Lstat(store.Path()); !os.IsNotExist(err) {
		t.Errorf("dangling canonical artifact still exists: %v", err)
	}
}

func TestFreshRootInvalidTimerStateDoesNotBlockIndependentCommands(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 7, 31, 12, 0, 0, 0, time.UTC)
	store := runningtimer.NewStore(t.TempDir())
	if err := os.MkdirAll(filepath.Dir(store.Path()), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(store.Path(), []byte("invalid"), 0o600); err != nil {
		t.Fatal(err)
	}
	timerService := apptimer.New(nil, nil, store, &countingRecoveryRecorder{}, func() time.Time { return now })

	tests := []struct {
		name    string
		args    []string
		options func(*bytes.Buffer, *bytes.Buffer) cli.RootOptions
	}{
		{
			name: "configure", args: []string{"configure"},
			options: func(stdout, stderr *bytes.Buffer) cli.RootOptions {
				return cli.RootOptions{
					Timer: timerService, Configure: &fakeConfigureRunner{},
					Prompter: &fakePrompter{lines: []string{"https://example.atlassian.net", "me@example.com", "1"}, secret: "token"},
					Out:      stdout, Err: stderr,
				}
			},
		},
		{
			name: "log", args: []string{"log", "CLOCK-15", "1h"},
			options: func(stdout, stderr *bytes.Buffer) cli.RootOptions {
				return cli.RootOptions{Timer: timerService, Log: &fakeLogRunner{result: submittedLogResult(now)}, Out: stdout, Err: stderr, Now: func() time.Time { return now }}
			},
		},
		{
			name: "report", args: []string{"report", "today"},
			options: func(stdout, stderr *bytes.Buffer) cli.RootOptions {
				return cli.RootOptions{Timer: timerService, Report: &fakeReportRunner{result: emptyReportResult(t, domainreport.Today, time.UTC)}, Out: stdout, Err: stderr, Now: func() time.Time { return now }, Location: time.UTC}
			},
		},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			var stdout, stderr bytes.Buffer
			root := cli.NewRoot(test.options(&stdout, &stderr))
			root.SetArgs(test.args)
			if exit := cli.Execute(root); exit != 0 {
				t.Fatalf("%s exit = %d, stderr = %q", test.name, exit, stderr.String())
			}
			if _, err := os.Stat(store.Path()); err != nil {
				t.Errorf("invalid timer state changed: %v", err)
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
	discardInput  apptimer.DiscardInput
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
func (f *fakeTimerRunner) Discard(input apptimer.DiscardInput) (apptimer.DiscardResult, error) {
	f.discardInput = input
	return f.discardResult, f.discardErr
}

type countingRecoveryRecorder struct {
	calls int
}

type staticConfiguration struct {
	value config.Configuration
}

func (s staticConfiguration) Load() (config.Configuration, error) { return s.value, nil }

type staticCredentials struct{}

func (staticCredentials) Get(credential.IdentityKey) (secret.Token, error) {
	return secret.Token{}, errors.New("unexpected credential access")
}

func (r *countingRecoveryRecorder) ResolveTimerStart(context.Context, recording.Auth, recording.TimingMode, time.Time, time.Time) (time.Time, error) {
	r.calls++
	return time.Time{}, errors.New("unexpected Jira contact")
}

func (r *countingRecoveryRecorder) Prepare(context.Context, recording.Auth, recording.Request, time.Time) (worklog.Draft, error) {
	r.calls++
	return worklog.Draft{}, errors.New("unexpected Jira contact")
}

func (r *countingRecoveryRecorder) Submit(context.Context, recording.Auth, worklog.Draft) recording.Result {
	r.calls++
	return recording.Result{Status: recording.Rejected, Cause: errors.New("unexpected Jira contact")}
}
