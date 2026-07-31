package runningtimer_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/JirakLu/clock/internal/jiraidentity"
	"github.com/JirakLu/clock/internal/runningtimer"
	"github.com/JirakLu/clock/internal/worklog"
)

func TestStoreCreatesInspectsConsumesAndDiscardsStrictState(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, time.July, 31, 10, 0, 0, 500_000_000, time.FixedZone("CEST", 7200))
	store := runningtimer.NewStore(t.TempDir())
	timer := testTimer(now.Add(-90 * time.Minute))
	if err := store.Create(timer, now); err != nil {
		t.Fatalf("Create() error = %v", err)
	}

	data, err := os.ReadFile(store.Path())
	if err != nil {
		t.Fatal(err)
	}
	text := string(data)
	for _, want := range []string{
		`"schema_version":1`, `"issue_key":"CLOCK-14"`,
		`"started_at":"2026-07-31T08:30:00.5+02:00"`,
		`"description":"Timer work"`, `"jira_cloud_id":"cloud"`,
		`"jira_account_id":"account"`,
	} {
		if !strings.Contains(text, want) {
			t.Errorf("state = %q, want %q", text, want)
		}
	}
	if info, err := os.Stat(store.Path()); err != nil || info.Mode().Perm() != 0o600 {
		t.Fatalf("state permissions = %v, error = %v", info.Mode().Perm(), err)
	}
	if info, err := os.Stat(filepath.Dir(store.Path())); err != nil || info.Mode().Perm() != 0o700 {
		t.Fatalf("directory permissions = %v, error = %v", info.Mode().Perm(), err)
	}

	inspection, err := store.Inspect(now)
	if err != nil {
		t.Fatalf("Inspect() error = %v", err)
	}
	if inspection.Timer.Issue != timer.Issue ||
		!inspection.Timer.StartedAt.Equal(timer.StartedAt) ||
		inspection.Timer.Description != timer.Description ||
		inspection.Timer.CloudID != timer.CloudID || inspection.Timer.AccountID != timer.AccountID {
		t.Errorf("Inspect() timer = %#v, want %#v", inspection.Timer, timer)
	}
	if err := store.Create(testTimer(now), now); err == nil || !strings.Contains(err.Error(), "already active") {
		t.Fatalf("second Create() error = %v", err)
	}
	if err := store.Consume(timer, now); err != nil {
		t.Fatalf("Consume() error = %v", err)
	}
	if _, err := store.Inspect(now); !strings.Contains(err.Error(), "No Running timer") {
		t.Fatalf("Inspect() after consume error = %v", err)
	}
}

func TestStoreRejectsInvalidAndIncompleteStateWithoutMutation(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, time.July, 31, 10, 0, 0, 0, time.UTC)
	tests := []struct {
		name string
		data string
		want string
	}{
		{"unknown field", `{"schema_version":1,"issue_key":"CLOCK-14","started_at":"2026-07-31T09:00:00Z","jira_cloud_id":"cloud","jira_account_id":"account","extra":true}`, "unknown field"},
		{"duplicate field", `{"schema_version":1,"issue_key":"CLOCK-14","issue_key":"CLOCK-15","started_at":"2026-07-31T09:00:00Z","jira_cloud_id":"cloud","jira_account_id":"account"}`, "duplicate field"},
		{"unsupported schema", `{"schema_version":2,"issue_key":"CLOCK-14","started_at":"2026-07-31T09:00:00Z","jira_cloud_id":"cloud","jira_account_id":"account"}`, "schema_version"},
		{"unnormalized issue", `{"schema_version":1,"issue_key":"clock-14","started_at":"2026-07-31T09:00:00Z","jira_cloud_id":"cloud","jira_account_id":"account"}`, "normalized"},
		{"future start", `{"schema_version":1,"issue_key":"CLOCK-14","started_at":"2026-07-31T11:00:00Z","jira_cloud_id":"cloud","jira_account_id":"account"}`, "future"},
		{"null description", `{"schema_version":1,"issue_key":"CLOCK-14","started_at":"2026-07-31T09:00:00Z","description":null,"jira_cloud_id":"cloud","jira_account_id":"account"}`, "must be a string"},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			store := runningtimer.NewStore(t.TempDir())
			if err := os.MkdirAll(filepath.Dir(store.Path()), 0o700); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(store.Path(), []byte(test.data), 0o600); err != nil {
				t.Fatal(err)
			}
			_, err := store.Inspect(now)
			if err == nil || !strings.Contains(err.Error(), store.Path()) || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("Inspect() error = %v, want path and %q", err, test.want)
			}
			if _, statErr := os.Stat(store.Path()); statErr != nil {
				t.Fatalf("invalid canonical state was mutated: %v", statErr)
			}
		})
	}
}

func TestStoreFailsClosedWhenStagingStateExists(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, time.July, 31, 10, 0, 0, 0, time.UTC)
	store := runningtimer.NewStore(t.TempDir())
	timer := testTimer(now.Add(-time.Hour))
	if err := store.Create(timer, now); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(store.StagingPath(), []byte("orphan"), 0o600); err != nil {
		t.Fatal(err)
	}
	_, err := store.Inspect(now)
	if err == nil || !strings.Contains(err.Error(), store.StagingPath()) {
		t.Fatalf("Inspect() error = %v, want staging path", err)
	}
	if _, err := os.Stat(store.StagingPath()); err != nil {
		t.Errorf("staging artifact was mutated: %v", err)
	}
}

func TestConsumptionLockFailurePreservesCanonicalState(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, time.July, 31, 10, 0, 0, 0, time.UTC)
	store := runningtimer.NewStore(t.TempDir())
	timer := testTimer(now.Add(-time.Hour))
	if err := store.Create(timer, now); err != nil {
		t.Fatal(err)
	}
	lockPath := store.Path() + ".lock"
	if err := os.Remove(lockPath); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(lockPath, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := store.Consume(timer, now); err == nil || !strings.Contains(err.Error(), "lock") {
		t.Fatalf("Consume() error = %v", err)
	}
	if _, err := os.Stat(store.Path()); err != nil {
		t.Fatalf("canonical state was not preserved: %v", err)
	}
}

func testTimer(start time.Time) runningtimer.Timer {
	return runningtimer.Timer{
		Issue: "CLOCK-14", StartedAt: start, Description: "Timer work",
		CloudID: jiraidentity.CloudID("cloud"), AccountID: jiraidentity.AccountID("account"),
	}
}

var _ worklog.IssueKey = testTimer(time.Time{}).Issue
