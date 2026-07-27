package recording_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/JirakLu/clock/internal/app/recording"
	"github.com/JirakLu/clock/internal/jiraidentity"
	"github.com/JirakLu/clock/internal/secret"
	"github.com/JirakLu/clock/internal/worklog"
)

func TestRecordCreatesManualWorklogEndingNow(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, time.July, 27, 11, 7, 23, 0, time.FixedZone("CEST", 2*60*60))
	duration, _ := worklog.ParseCompactDuration("30m")
	gateway := &fakeGateway{}
	service := recording.New(gateway)

	result, err := service.Record(context.Background(), testAuth(), recording.Request{
		Issue: "CLOCK-13", Timing: recording.Timing{
			Mode: recording.EndingNow, Duration: duration,
		}, Description: "Implementation",
	}, now)
	if err != nil {
		t.Fatalf("Record() error = %v", err)
	}
	if result.Status != recording.Submitted {
		t.Fatalf("Record() status = %v", result.Status)
	}
	if result.Worklog.ID != "created" || result.Worklog.AuthorID != "account" {
		t.Errorf("Record() did not return Jira's authoritative Worklog: %#v", result.Worklog)
	}
	if gateway.created.Issue != "CLOCK-13" ||
		!gateway.created.Interval.Start().Equal(now.Add(-30*time.Minute)) ||
		!gateway.created.Interval.End().Equal(now) ||
		gateway.created.Description != "Implementation" {
		t.Errorf("created Worklog = %#v", gateway.created)
	}
}

func TestRecordAfterLastUsesTheAuthoredWorklogWhoseEndIsLatest(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, time.July, 27, 12, 0, 0, 0, time.UTC)
	gateway := &fakeGateway{listed: []worklog.Worklog{
		existingWorklog(t, "later-start", now.Add(-2*time.Hour), now.Add(-90*time.Minute)),
		existingWorklog(t, "latest-end", now.Add(-4*time.Hour), now.Add(-time.Hour)),
	}}

	result, err := recording.New(gateway).Record(
		context.Background(),
		testAuth(),
		recording.Request{
			Issue: "CLOCK-13", Timing: recording.Timing{Mode: recording.AfterLast},
		},
		now,
	)
	if err != nil {
		t.Fatalf("Record() error = %v", err)
	}
	if want := now.Add(-time.Hour); !result.Worklog.Interval.Start().Equal(want) {
		t.Errorf("created Worklog start = %v, want latest authored Worklog end %v", result.Worklog.Interval.Start(), want)
	}
}

func TestRecordExplicitStartAllowsAdjacentWorklog(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, time.July, 27, 11, 0, 0, 0, time.UTC)
	start := now.Add(-time.Hour)
	duration, _ := worklog.ParseCompactDuration("30m")
	adjacent := existingWorklog(t, "1", start.Add(-time.Hour), start)
	gateway := &fakeGateway{listed: []worklog.Worklog{adjacent}}

	result, err := recording.New(gateway).Record(
		context.Background(),
		testAuth(),
		recording.Request{
			Issue: "CLOCK-13",
			Timing: recording.Timing{
				Mode: recording.AtStart, Duration: duration, Start: start,
			},
		},
		now,
	)
	if err != nil {
		t.Fatalf("Record() error = %v", err)
	}
	if result.Status != recording.Submitted || gateway.createCalls != 1 {
		t.Errorf("result = %#v, create calls = %d", result, gateway.createCalls)
	}
}

func TestRecordAfterLastBeginsAtLatestAuthoredWorklogEnd(t *testing.T) {
	t.Parallel()

	location := time.FixedZone("CEST", 2*60*60)
	now := time.Date(2026, time.July, 27, 11, 0, 0, 0, location)
	gateway := &fakeGateway{listed: []worklog.Worklog{
		existingWorklog(t, "early", time.Date(2026, time.July, 27, 8, 0, 0, 0, location), time.Date(2026, time.July, 27, 9, 0, 0, 0, location)),
		existingWorklog(t, "latest", time.Date(2026, time.July, 27, 9, 30, 0, 0, location), time.Date(2026, time.July, 27, 10, 15, 0, 0, location)),
	}}

	result, err := recording.New(gateway).Record(
		context.Background(),
		testAuth(),
		recording.Request{
			Issue: "CLOCK-13", Timing: recording.Timing{Mode: recording.AfterLast},
		},
		now,
	)
	if err != nil {
		t.Fatalf("Record() error = %v", err)
	}
	wantStart := time.Date(2026, time.July, 27, 10, 15, 0, 0, location)
	if result.Status != recording.Submitted ||
		!result.Worklog.Interval.Start().Equal(wantStart) ||
		!result.Worklog.Interval.End().Equal(now) {
		t.Errorf("result = %#v", result)
	}
}

func TestRecordRejectsInvalidOrOverlappingIntervalsBeforeMutation(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, time.July, 27, 11, 0, 0, 0, time.UTC)
	duration, _ := worklog.ParseCompactDuration("1h")
	tests := []struct {
		name    string
		timing  recording.Timing
		listed  []worklog.Worklog
		wantErr error
	}{
		{
			name: "future explicit end",
			timing: recording.Timing{
				Mode: recording.AtStart, Duration: duration, Start: now.Add(-30 * time.Minute),
			},
			wantErr: recording.ErrFutureWorklog,
		},
		{
			name: "overlap",
			timing: recording.Timing{
				Mode: recording.EndingNow, Duration: duration,
			},
			listed: []worklog.Worklog{
				existingWorklog(t, "existing", now.Add(-90*time.Minute), now.Add(-30*time.Minute)),
			},
			wantErr: recording.ErrOverlap,
		},
		{
			name: "no latest Worklog",
			timing: recording.Timing{
				Mode: recording.AfterLast,
			},
			wantErr: recording.ErrNoLatestWorklog,
		},
		{
			name: "inferred interval below one second",
			timing: recording.Timing{
				Mode: recording.AfterLast,
			},
			listed: []worklog.Worklog{
				existingWorklog(t, "latest", now.Add(-time.Hour), now.Add(-500*time.Millisecond)),
			},
			wantErr: recording.ErrNonPositiveWorklog,
		},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			gateway := &fakeGateway{listed: test.listed}
			_, err := recording.New(gateway).Record(
				context.Background(),
				testAuth(),
				recording.Request{Issue: "CLOCK-13", Timing: test.timing},
				now,
			)
			if !errors.Is(err, test.wantErr) {
				t.Errorf("Record() error = %v, want %v", err, test.wantErr)
			}
			if gateway.createCalls != 0 {
				t.Errorf("CreateWorklog() called %d times", gateway.createCalls)
			}
		})
	}
}

func TestRecordReturnsTypedRecoveryResultForJiraFailure(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, time.July, 27, 11, 0, 0, 0, time.UTC)
	duration, _ := worklog.ParseCompactDuration("30m")
	for _, test := range []struct {
		name       string
		createErr  error
		wantStatus recording.Status
	}{
		{
			name:       "definite rejection",
			createErr:  submissionError{cause: errors.New("forbidden")},
			wantStatus: recording.Rejected,
		},
		{
			name:       "uncertain outcome",
			createErr:  submissionError{cause: errors.New("connection reset"), uncertain: true},
			wantStatus: recording.Uncertain,
		},
	} {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			gateway := &fakeGateway{createErr: test.createErr}
			result, err := recording.New(gateway).Record(
				context.Background(),
				testAuth(),
				recording.Request{
					Issue: "CLOCK-13",
					Timing: recording.Timing{
						Mode: recording.EndingNow, Duration: duration,
					},
					Description: "Recovery description",
				},
				now,
			)
			if err != nil {
				t.Fatalf("Record() error = %v", err)
			}
			if result.Status != test.wantStatus ||
				result.Attempt.Issue != "CLOCK-13" ||
				result.Attempt.Description != "Recovery description" ||
				result.Cause == nil {
				t.Errorf("result = %#v", result)
			}
		})
	}
}

func testAuth() recording.Auth {
	token, _ := secret.NewToken("api-token")
	return recording.Auth{
		Identity: jiraidentity.Reference{
			SiteURL: "https://example.atlassian.net", CloudID: "cloud",
			Email: "person@example.com", AccountID: "account",
		},
		Token: token,
	}
}

func existingWorklog(t *testing.T, id string, start, end time.Time) worklog.Worklog {
	t.Helper()
	interval, err := worklog.NewInterval(start, end)
	if err != nil {
		t.Fatal(err)
	}
	return worklog.Worklog{ID: id, Issue: "OTHER-1", AuthorID: "account", Interval: interval}
}

type fakeGateway struct {
	listed      []worklog.Worklog
	listErr     error
	created     worklog.Draft
	createErr   error
	createCalls int
}

func (f *fakeGateway) VerifyIdentity(context.Context, recording.Auth) error {
	return nil
}

func (f *fakeGateway) ListAuthoredWorklogs(
	context.Context,
	recording.Auth,
	time.Time,
	time.Time,
) ([]worklog.Worklog, error) {
	return f.listed, f.listErr
}

func (f *fakeGateway) CreateWorklog(
	_ context.Context,
	_ recording.Auth,
	draft worklog.Draft,
) (worklog.Worklog, error) {
	f.createCalls++
	f.created = draft
	if f.createErr != nil {
		return worklog.Worklog{}, f.createErr
	}
	return worklog.Worklog{
		ID: "created", Issue: draft.Issue, AuthorID: "account",
		Interval: draft.Interval, Description: draft.Description,
	}, nil
}

type submissionError struct {
	cause     error
	uncertain bool
}

func (e submissionError) Error() string {
	return e.cause.Error()
}

func (e submissionError) Uncertain() bool {
	return e.uncertain
}
