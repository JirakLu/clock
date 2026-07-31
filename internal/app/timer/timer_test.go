package timer_test

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/JirakLu/clock/internal/app/recording"
	apptimer "github.com/JirakLu/clock/internal/app/timer"
	"github.com/JirakLu/clock/internal/config"
	"github.com/JirakLu/clock/internal/credential"
	"github.com/JirakLu/clock/internal/jiraidentity"
	"github.com/JirakLu/clock/internal/runningtimer"
	"github.com/JirakLu/clock/internal/secret"
	"github.com/JirakLu/clock/internal/worklog"
)

func TestStartStoresOneResolvedIdentityBoundRunningTimer(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 7, 31, 10, 0, 0, 500_000_000, time.UTC)
	resolved := now.Add(-time.Hour)
	state := &fakeState{inspectErr: runningtimer.ErrNoTimer}
	recorder := &fakeRecorder{resolvedStart: resolved}
	service := testService(state, recorder, now)

	result, err := service.Start(context.Background(), apptimer.StartInput{
		Issue: "CLOCK-14", Mode: recording.AfterLast, Description: "Lifecycle",
	})
	if err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	if result.Timer.Issue != "CLOCK-14" || !result.Timer.StartedAt.Equal(resolved) ||
		result.Timer.Description != "Lifecycle" || result.Timer.CloudID != "cloud" ||
		result.Timer.AccountID != "account" || state.createCalls != 1 {
		t.Errorf("Start() result/state = %#v / %#v", result, state.created)
	}
}

func TestSecondStartReturnsActiveTimerWithoutMutationOrJiraContact(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 7, 31, 10, 0, 0, 0, time.UTC)
	active := testTimer(now.Add(-time.Hour))
	state := &fakeState{inspection: runningtimer.Inspection{Timer: active}}
	recorder := &fakeRecorder{}
	_, err := testService(state, recorder, now).Start(context.Background(), apptimer.StartInput{Issue: "OTHER-1", Mode: recording.EndingNow})
	var already *apptimer.AlreadyRunningError
	if !errors.As(err, &already) || already.Timer.Issue != active.Issue || state.createCalls != 0 || recorder.resolveCalls != 0 {
		t.Fatalf("Start() error/state = %v / %#v", err, state)
	}
}

func TestConcurrentSecondStartReturnsTheWinningActiveTimer(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 7, 31, 10, 0, 0, 0, time.UTC)
	winner := testTimer(now)
	state := &fakeState{
		inspectionSequence: []inspectionResult{
			{err: runningtimer.ErrNoTimer},
			{inspection: runningtimer.Inspection{Timer: winner}},
		},
		createErr: errors.New("already active"),
	}
	recorder := &fakeRecorder{resolvedStart: now}
	_, err := testService(state, recorder, now).Start(context.Background(), apptimer.StartInput{Issue: "OTHER-1", Mode: recording.EndingNow})
	var already *apptimer.AlreadyRunningError
	if !errors.As(err, &already) || already.Timer.Issue != winner.Issue || state.createCalls != 1 {
		t.Fatalf("Start() error/state = %v / %#v", err, state)
	}
}

func TestStatusReportsNoTimerAndIdentityMismatch(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 7, 31, 10, 0, 0, 0, time.UTC)
	none, err := testService(&fakeState{inspectErr: runningtimer.ErrNoTimer}, &fakeRecorder{}, now).Status()
	if err != nil || none.Active {
		t.Fatalf("Status(no timer) = %#v, %v", none, err)
	}
	active := testTimer(now.Add(-90 * time.Minute))
	state := &fakeState{inspection: runningtimer.Inspection{Timer: active}}
	service := apptimer.New(
		fakeConfig{configuration: config.Configuration{JiraIdentity: jiraidentity.Reference{CloudID: "other", AccountID: "other"}}},
		fakeCredentials{}, state, &fakeRecorder{}, func() time.Time { return now },
	)
	status, err := service.Status()
	if err != nil || !status.Active || !status.IdentityMismatch || status.ElapsedSeconds != 5400 {
		t.Fatalf("Status(active) = %#v, %v", status, err)
	}
}

func TestStopPreservesStateUntilValidationThenConsumesBeforeSubmission(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 7, 31, 10, 0, 0, 900_000_000, time.UTC)
	active := testTimer(now.Add(-time.Hour - 400*time.Millisecond))
	state := &fakeState{inspection: runningtimer.Inspection{Timer: active}}
	order := []string{}
	recorder := &fakeRecorder{order: &order, result: recording.Result{Status: recording.Submitted}}
	state.order = &order
	service := testService(state, recorder, now)

	result, err := service.Stop(context.Background(), apptimer.StopInput{})
	if err != nil {
		t.Fatalf("Stop() error = %v", err)
	}
	if got := result.Attempt.Interval.Seconds(); got != 3600 {
		t.Errorf("elapsed seconds = %d, want 3600", got)
	}
	if len(order) != 3 || order[0] != "prepare" || order[1] != "consume" || order[2] != "submit" {
		t.Errorf("operation order = %v", order)
	}
}

func TestStopValidationFailurePreservesRunningTimer(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 7, 31, 10, 0, 0, 0, time.UTC)
	state := &fakeState{inspection: runningtimer.Inspection{Timer: testTimer(now.Add(-time.Hour))}}
	recorder := &fakeRecorder{prepareErr: recording.ErrOverlap}
	_, err := testService(state, recorder, now).Stop(context.Background(), apptimer.StopInput{})
	if !errors.Is(err, recording.ErrOverlap) || state.consumeCalls != 0 || recorder.submitCalls != 0 {
		t.Fatalf("Stop() error/calls = %v, consume %d, submit %d", err, state.consumeCalls, recorder.submitCalls)
	}
}

func TestStopSubmissionFailureDoesNotRecreateConsumedState(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 7, 31, 10, 0, 0, 0, time.UTC)
	state := &fakeState{inspection: runningtimer.Inspection{Timer: testTimer(now.Add(-time.Hour))}}
	recorder := &fakeRecorder{result: recording.Result{Status: recording.Uncertain, Cause: errors.New("reset")}}
	result, err := testService(state, recorder, now).Stop(context.Background(), apptimer.StopInput{})
	if err != nil || result.Status != recording.Uncertain || state.consumeCalls != 1 || state.createCalls != 0 {
		t.Fatalf("Stop() = %#v, %v; state = %#v", result, err, state)
	}
}

func TestStopRejectsUnsafeConditionsBeforeJiraWrite(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 7, 31, 10, 0, 0, 500_000_000, time.UTC)
	for _, test := range []struct {
		name       string
		active     runningtimer.Timer
		consumeErr error
		want       string
	}{
		{name: "below one second", active: testTimer(now.Add(-500 * time.Millisecond)), want: "at least one whole second"},
		{name: "identity mismatch", active: runningtimer.Timer{Issue: "CLOCK-14", StartedAt: now.Add(-time.Hour), CloudID: "other", AccountID: "other"}, want: "different Jira"},
		{name: "consume failure", active: testTimer(now.Add(-time.Hour)), consumeErr: errors.New("lock failed"), want: "consume Running timer"},
	} {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			state := &fakeState{inspection: runningtimer.Inspection{Timer: test.active}, consumeErr: test.consumeErr}
			recorder := &fakeRecorder{}
			_, err := testService(state, recorder, now).Stop(context.Background(), apptimer.StopInput{})
			if err == nil || !strings.Contains(err.Error(), test.want) || recorder.submitCalls != 0 || state.createCalls != 0 {
				t.Fatalf("Stop() error/calls = %v, submit %d", err, recorder.submitCalls)
			}
		})
	}
}

func TestDiscardRemovesValidTimerWithoutLoadingJiraConfiguration(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 7, 31, 10, 0, 0, 0, time.UTC)
	active := testTimer(now.Add(-time.Hour))
	state := &fakeState{inspection: runningtimer.Inspection{Timer: active}}
	service := apptimer.New(fakeConfig{err: errors.New("must not load")}, fakeCredentials{}, state, &fakeRecorder{}, func() time.Time { return now })
	result, err := service.Discard(apptimer.DiscardInput{})
	if err != nil || result.Timer.Issue != active.Issue || state.discardCalls != 1 {
		t.Fatalf("Discard() = %#v, %v; state %#v", result, err, state)
	}
}

func testService(state *fakeState, recorder *fakeRecorder, now time.Time) *apptimer.Service {
	token, _ := secret.NewToken("token")
	return apptimer.New(
		fakeConfig{configuration: config.Configuration{JiraIdentity: jiraidentity.Reference{
			SiteURL: "https://example.atlassian.net", CloudID: "cloud", Email: "me@example.com", AccountID: "account",
		}}}, fakeCredentials{token: token}, state, recorder, func() time.Time { return now },
	)
}

func testTimer(start time.Time) runningtimer.Timer {
	return runningtimer.Timer{Issue: "CLOCK-14", StartedAt: start, Description: "Stored", CloudID: "cloud", AccountID: "account"}
}

type fakeConfig struct {
	configuration config.Configuration
	err           error
}

func (f fakeConfig) Load() (config.Configuration, error) { return f.configuration, f.err }

type fakeCredentials struct {
	token secret.Token
	err   error
}

func (f fakeCredentials) Get(credential.IdentityKey) (secret.Token, error) { return f.token, f.err }

type fakeState struct {
	inspection                runningtimer.Inspection
	inspectErr                error
	created                   runningtimer.Timer
	createCalls, consumeCalls int
	discardCalls              int
	order                     *[]string
	consumeErr                error
	createErr                 error
	inspectionSequence        []inspectionResult
}

type inspectionResult struct {
	inspection runningtimer.Inspection
	err        error
}

func (f *fakeState) Inspect(time.Time) (runningtimer.Inspection, error) {
	if len(f.inspectionSequence) != 0 {
		result := f.inspectionSequence[0]
		f.inspectionSequence = f.inspectionSequence[1:]
		return result.inspection, result.err
	}
	return f.inspection, f.inspectErr
}
func (f *fakeState) Create(timer runningtimer.Timer, _ time.Time) error {
	f.createCalls++
	f.created = timer
	return f.createErr
}
func (f *fakeState) Consume(runningtimer.Timer, time.Time) error {
	f.consumeCalls++
	if f.order != nil {
		*f.order = append(*f.order, "consume")
	}
	return f.consumeErr
}
func (f *fakeState) Discard(runningtimer.Timer, time.Time) error { f.discardCalls++; return nil }
func (f *fakeState) ForceDiscard(time.Time) (runningtimer.ForceDiscardResult, error) {
	return runningtimer.ForceDiscardResult{}, nil
}

type fakeRecorder struct {
	resolvedStart time.Time
	prepareErr    error
	result        recording.Result
	order         *[]string
	submitCalls   int
	resolveCalls  int
}

func (f *fakeRecorder) ResolveTimerStart(context.Context, recording.Auth, recording.TimingMode, time.Time, time.Time) (time.Time, error) {
	f.resolveCalls++
	return f.resolvedStart, nil
}
func (f *fakeRecorder) Prepare(_ context.Context, _ recording.Auth, request recording.Request, _ time.Time) (worklog.Draft, error) {
	if f.order != nil {
		*f.order = append(*f.order, "prepare")
	}
	if f.prepareErr != nil {
		return worklog.Draft{}, f.prepareErr
	}
	return worklog.Draft{Issue: request.Issue, Interval: mustInterval(request.Timing.Start, request.Timing.Start.Add(request.Timing.Duration.Duration())), Description: request.Description}, nil
}
func (f *fakeRecorder) Submit(_ context.Context, _ recording.Auth, draft worklog.Draft) recording.Result {
	f.submitCalls++
	if f.order != nil {
		*f.order = append(*f.order, "submit")
	}
	result := f.result
	if result.Status != recording.Submitted {
		result.Attempt = draft
	}
	return result
}
func mustInterval(start, end time.Time) worklog.Interval {
	interval, _ := worklog.NewInterval(start, end)
	return interval
}
