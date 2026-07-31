package runningtimer_test

import (
	"errors"
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
		{"malformed JSON", `{"schema_version":`, "parse"},
		{"non-object JSON", `[]`, "must be an object"},
		{"unknown field", `{"schema_version":1,"issue_key":"CLOCK-14","started_at":"2026-07-31T09:00:00Z","jira_cloud_id":"cloud","jira_account_id":"account","extra":true}`, "unknown field"},
		{"duplicate field", `{"schema_version":1,"issue_key":"CLOCK-14","issue_key":"CLOCK-15","started_at":"2026-07-31T09:00:00Z","jira_cloud_id":"cloud","jira_account_id":"account"}`, "duplicate field"},
		{"unsupported schema", `{"schema_version":2,"issue_key":"CLOCK-14","started_at":"2026-07-31T09:00:00Z","jira_cloud_id":"cloud","jira_account_id":"account"}`, "schema_version"},
		{"missing field", `{"schema_version":1,"issue_key":"CLOCK-14","started_at":"2026-07-31T09:00:00Z","jira_cloud_id":"cloud"}`, "required field"},
		{"invalid field type", `{"schema_version":1,"issue_key":"CLOCK-14","started_at":9,"jira_cloud_id":"cloud","jira_account_id":"account"}`, "must be a string"},
		{"unnormalized issue", `{"schema_version":1,"issue_key":"clock-14","started_at":"2026-07-31T09:00:00Z","jira_cloud_id":"cloud","jira_account_id":"account"}`, "normalized"},
		{"empty cloud ID", `{"schema_version":1,"issue_key":"CLOCK-14","started_at":"2026-07-31T09:00:00Z","jira_cloud_id":" ","jira_account_id":"account"}`, "must not be empty"},
		{"empty account ID", `{"schema_version":1,"issue_key":"CLOCK-14","started_at":"2026-07-31T09:00:00Z","jira_cloud_id":"cloud","jira_account_id":""}`, "must not be empty"},
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
			if err == nil || !strings.Contains(err.Error(), store.Path()) || !strings.Contains(err.Error(), test.want) || !strings.Contains(err.Error(), "clock discard --force") {
				t.Fatalf("Inspect() error = %v, want path, %q, and forced-discard guidance", err, test.want)
			}
			if _, statErr := os.Stat(store.Path()); statErr != nil {
				t.Fatalf("invalid canonical state was mutated: %v", statErr)
			}
		})
	}
}

func TestStoreDoesNotExpireStructurallyValidState(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, time.July, 31, 10, 0, 0, 0, time.UTC)
	store := runningtimer.NewStore(t.TempDir())
	timer := testTimer(now.AddDate(-10, 0, 0))
	if err := store.Create(timer, now); err != nil {
		t.Fatal(err)
	}
	oldModificationTime := now.AddDate(-20, 0, 0)
	if err := os.Chtimes(store.Path(), oldModificationTime, oldModificationTime); err != nil {
		t.Fatal(err)
	}
	inspection, err := store.Inspect(now)
	if err != nil || inspection.Timer != timer {
		t.Fatalf("Inspect() = %#v, %v; old valid state must remain active", inspection, err)
	}
}

func TestStoreKeepsValidCanonicalStateAndRemovesOrphanStagingState(t *testing.T) {
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
	inspection, err := store.Inspect(now)
	if err != nil {
		t.Fatalf("Inspect() error = %v", err)
	}
	if inspection.Timer != timer {
		t.Errorf("Inspect() timer = %#v, want %#v", inspection.Timer, timer)
	}
	if len(inspection.Warnings) != 1 || !strings.Contains(inspection.Warnings[0], store.StagingPath()) || !strings.Contains(inspection.Warnings[0], "removed") {
		t.Errorf("Inspect() warnings = %q, want removed staging path", inspection.Warnings)
	}
	if _, err := os.Stat(store.StagingPath()); !os.IsNotExist(err) {
		t.Errorf("staging artifact still exists: %v", err)
	}
}

func TestStoreFailsClosedWhenOnlyStagingStateExists(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, time.July, 31, 10, 0, 0, 0, time.UTC)
	store := runningtimer.NewStore(t.TempDir())
	if err := os.MkdirAll(filepath.Dir(store.StagingPath()), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(store.StagingPath(), []byte("orphan"), 0o600); err != nil {
		t.Fatal(err)
	}
	_, err := store.Inspect(now)
	if err == nil || !strings.Contains(err.Error(), store.StagingPath()) || !strings.Contains(err.Error(), "clock discard --force") {
		t.Fatalf("Inspect() error = %v, want staging path and forced-discard guidance", err)
	}
}

func TestStoreDiagnosesDanglingCanonicalAndStagingArtifacts(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, time.July, 31, 10, 0, 0, 0, time.UTC)
	for _, artifact := range []string{"canonical", "staging"} {
		artifact := artifact
		t.Run(artifact, func(t *testing.T) {
			t.Parallel()
			store := runningtimer.NewStore(t.TempDir())
			path := store.Path()
			if artifact == "staging" {
				path = store.StagingPath()
			}
			if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
				t.Fatal(err)
			}
			if err := os.Symlink("missing-target", path); err != nil {
				t.Fatal(err)
			}
			_, err := store.Inspect(now)
			if err == nil || !strings.Contains(err.Error(), path) || !strings.Contains(err.Error(), "clock discard --force") {
				t.Fatalf("Inspect() error = %v", err)
			}
			if _, err := os.Lstat(path); err != nil {
				t.Errorf("dangling artifact was mutated: %v", err)
			}
		})
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

func TestForceDiscardPreservesValidCanonicalStateAndRemovesOrphanStaging(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, time.July, 31, 10, 0, 0, 0, time.UTC)
	store := runningtimer.NewStore(t.TempDir())
	if err := store.Create(testTimer(now.Add(-time.Hour)), now); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(store.StagingPath(), []byte("orphan"), 0o600); err != nil {
		t.Fatal(err)
	}
	result, err := store.ForceDiscard(now)
	if err == nil || !strings.Contains(err.Error(), "valid") || !strings.Contains(err.Error(), "clock discard") {
		t.Fatalf("ForceDiscard() error = %v", err)
	}
	if len(result.Removed) != 1 || result.Removed[0] != store.StagingPath() {
		t.Errorf("ForceDiscard() removed = %q, want orphan staging path", result.Removed)
	}
	if _, err := os.Stat(store.Path()); err != nil {
		t.Errorf("valid canonical state was mutated: %v", err)
	}
	if _, err := os.Lstat(store.StagingPath()); !os.IsNotExist(err) {
		t.Errorf("orphan staging state still exists: %v", err)
	}
}

func TestForceDiscardRemovesInvalidCanonicalAndStagingArtifacts(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	store := runningtimer.NewStore(root)
	if err := os.MkdirAll(filepath.Dir(store.Path()), 0o700); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{store.Path(), store.StagingPath()} {
		if err := os.WriteFile(path, []byte("not JSON"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	result, err := store.ForceDiscard(time.Now())
	if err != nil {
		t.Fatalf("ForceDiscard() error = %v", err)
	}
	if strings.Join(result.Removed, "|") != store.Path()+"|"+store.StagingPath() {
		t.Errorf("ForceDiscard() removed = %q", result.Removed)
	}
	for _, path := range result.Removed {
		if _, err := os.Stat(path); !os.IsNotExist(err) {
			t.Errorf("removed artifact %q still exists: %v", path, err)
		}
	}
}

func TestForceDiscardReportsPartialRemovalAndContinues(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	store := runningtimer.NewStoreWithOptions(root, runningtimer.StoreOptions{Remove: func(path string) error {
		if strings.HasSuffix(path, ".staging") {
			return os.Remove(path)
		}
		return errors.New("injected removal failure")
	}})
	if err := os.MkdirAll(filepath.Dir(store.Path()), 0o700); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{store.Path(), store.StagingPath()} {
		if err := os.WriteFile(path, []byte("invalid"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	result, err := store.ForceDiscard(time.Now())
	if err == nil || !strings.Contains(err.Error(), store.Path()) || !strings.Contains(err.Error(), "incomplete") {
		t.Fatalf("ForceDiscard() error = %v", err)
	}
	if len(result.Removed) != 1 || result.Removed[0] != store.StagingPath() {
		t.Errorf("ForceDiscard() removed = %q", result.Removed)
	}
	if _, err := os.Stat(store.Path()); err != nil {
		t.Errorf("failed canonical removal did not preserve artifact: %v", err)
	}
	if _, err := os.Stat(store.StagingPath()); !os.IsNotExist(err) {
		t.Errorf("staging removal was not attempted: %v", err)
	}
}

func TestForceDiscardFailsWhenNoStateArtifactsExist(t *testing.T) {
	t.Parallel()

	store := runningtimer.NewStore(t.TempDir())
	result, err := store.ForceDiscard(time.Now())
	if err == nil || !strings.Contains(err.Error(), "no Running timer state artifacts") || len(result.Removed) != 0 {
		t.Fatalf("ForceDiscard() = %#v, %v", result, err)
	}
}

func TestForceDiscardRemovesDanglingArtifactsWithoutClaimingEarlyCompletion(t *testing.T) {
	t.Parallel()

	store := runningtimer.NewStore(t.TempDir())
	if err := os.MkdirAll(filepath.Dir(store.Path()), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(store.Path(), []byte("invalid"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("missing-target", store.StagingPath()); err != nil {
		t.Fatal(err)
	}
	result, err := store.ForceDiscard(time.Now())
	if err != nil {
		t.Fatalf("ForceDiscard() error = %v", err)
	}
	if strings.Join(result.Removed, "|") != store.Path()+"|"+store.StagingPath() {
		t.Errorf("ForceDiscard() removed = %q", result.Removed)
	}
	for _, path := range result.Removed {
		if _, err := os.Lstat(path); !os.IsNotExist(err) {
			t.Errorf("artifact %q still exists: %v", path, err)
		}
	}
}

func testTimer(start time.Time) runningtimer.Timer {
	return runningtimer.Timer{
		Issue: "CLOCK-14", StartedAt: start, Description: "Timer work",
		CloudID: jiraidentity.CloudID("cloud"), AccountID: jiraidentity.AccountID("account"),
	}
}

var _ worklog.IssueKey = testTimer(time.Time{}).Issue
