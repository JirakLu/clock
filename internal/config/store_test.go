package config_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/JirakLu/clock/internal/config"
	"github.com/JirakLu/clock/internal/earnings"
	"github.com/JirakLu/clock/internal/jiraidentity"
)

func TestStoreSavesAndLoadsInspectableConfiguration(t *testing.T) {
	t.Parallel()

	rate, err := earnings.ParseHourlyRate("750.00")
	if err != nil {
		t.Fatal(err)
	}
	store := config.NewStore(t.TempDir())
	want := config.Configuration{
		JiraIdentity: jiraidentity.Reference{
			SiteURL: "https://example.atlassian.net", CloudID: "cloud-123",
			Email: "person@example.com", AccountID: "account-456",
		},
		HourlyRate: rate,
	}

	if err := store.Save(want); err != nil {
		t.Fatalf("Save() error = %v", err)
	}

	data, err := os.ReadFile(store.Path())
	if err != nil {
		t.Fatal(err)
	}
	text := string(data)
	for _, expected := range []string{
		`[jira]`,
		`site_url = "https://example.atlassian.net"`,
		`cloud_id = "cloud-123"`,
		`email = "person@example.com"`,
		`account_id = "account-456"`,
		`[earnings]`,
		`hourly_rate_czk = "750.00"`,
	} {
		if !strings.Contains(text, expected) {
			t.Errorf("config file does not contain %q:\n%s", expected, text)
		}
	}
	if strings.Contains(strings.ToLower(text), "token") {
		t.Errorf("config file contains a token field:\n%s", text)
	}

	info, err := os.Stat(store.Path())
	if err != nil {
		t.Fatal(err)
	}
	if got := info.Mode().Perm(); got != 0o600 {
		t.Errorf("config permissions = %o, want 600", got)
	}
	directoryInfo, err := os.Stat(filepath.Dir(store.Path()))
	if err != nil {
		t.Fatal(err)
	}
	if got := directoryInfo.Mode().Perm(); got != 0o700 {
		t.Errorf("configuration directory permissions = %o, want 700", got)
	}

	got, err := store.Load()
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if got != want {
		t.Errorf("Load() = %#v, want %#v", got, want)
	}
}

func TestStoreRejectsMalformedDirectEditWithPath(t *testing.T) {
	t.Parallel()

	store := config.NewStore(t.TempDir())
	if err := os.MkdirAll(filepath.Dir(store.Path()), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(store.Path(), []byte("[jira]\nsite_url = 3\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	_, err := store.Load()
	if err == nil {
		t.Fatal("Load() unexpectedly succeeded")
	}
	if !strings.Contains(err.Error(), store.Path()) {
		t.Errorf("Load() error = %q, want configuration path", err)
	}
}

func TestStoreRejectsUnknownDirectEditFields(t *testing.T) {
	t.Parallel()

	store := config.NewStore(t.TempDir())
	data := `[jira]
site_url = "https://example.atlassian.net"
cloud_id = "cloud-123"
email = "person@example.com"
account_id = "account-456"
token = "must-not-be-accepted"

[earnings]
hourly_rate_czk = "750.00"
`
	if err := os.MkdirAll(filepath.Dir(store.Path()), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(store.Path(), []byte(data), 0o600); err != nil {
		t.Fatal(err)
	}

	if _, err := store.Load(); err == nil {
		t.Fatal("Load() unexpectedly accepted an unknown token field")
	}
}

func TestFailedSaveDoesNotReplaceWorkingConfiguration(t *testing.T) {
	t.Parallel()

	rate, _ := earnings.ParseHourlyRate("500")
	store := config.NewStore(t.TempDir())
	working := config.Configuration{
		JiraIdentity: jiraidentity.Reference{
			SiteURL: "https://example.atlassian.net", CloudID: "cloud",
			Email: "old@example.com", AccountID: "old-account",
		},
		HourlyRate: rate,
	}
	if err := store.Save(working); err != nil {
		t.Fatal(err)
	}
	before, err := os.ReadFile(store.Path())
	if err != nil {
		t.Fatal(err)
	}

	invalid := working
	invalid.JiraIdentity.SiteURL = "http://insecure.example.com"
	if err := store.Save(invalid); err == nil {
		t.Fatal("Save() unexpectedly accepted invalid configuration")
	}
	after, err := os.ReadFile(store.Path())
	if err != nil {
		t.Fatal(err)
	}
	if string(after) != string(before) {
		t.Fatal("failed Save() replaced the working configuration")
	}
}

func TestPersistenceFailureDoesNotReplaceWorkingConfiguration(t *testing.T) {
	rate, _ := earnings.ParseHourlyRate("500")
	store := config.NewStore(t.TempDir())
	working := config.Configuration{
		JiraIdentity: jiraidentity.Reference{
			SiteURL: "https://example.atlassian.net", CloudID: "cloud",
			Email: "old@example.com", AccountID: "old-account",
		},
		HourlyRate: rate,
	}
	if err := store.Save(working); err != nil {
		t.Fatal(err)
	}
	directory := filepath.Dir(store.Path())
	if err := os.Chmod(directory, 0o500); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = os.Chmod(directory, 0o700)
	})

	replacement := working
	replacement.JiraIdentity.Email = "new@example.com"
	if err := store.Save(replacement); err == nil {
		t.Fatal("Save() unexpectedly succeeded with an unwritable configuration directory")
	}

	got, err := store.Load()
	if err != nil {
		t.Fatal(err)
	}
	if got != working {
		t.Errorf("working configuration was replaced: got %#v, want %#v", got, working)
	}
}
