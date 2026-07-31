package main

import (
	"bytes"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/JirakLu/clock/internal/runningtimer"
)

func TestMachineLocationResolvesAnIANATimezone(t *testing.T) {
	t.Setenv("TZ", "Europe/Prague")

	location, err := machineLocation()
	if err != nil {
		t.Fatalf("machineLocation() error = %v", err)
	}
	if got := location.String(); got != "Europe/Prague" {
		t.Errorf("machineLocation() = %q, want Europe/Prague", got)
	}
}

func TestCompiledBinaryReportsEmbeddedVersionAndHelp(t *testing.T) {
	t.Parallel()

	executable := filepath.Join(t.TempDir(), "clock")
	build := exec.Command(
		"go", "build",
		"-ldflags", "-X main.version=v1.2.3 -X main.revision=abc123",
		"-o", executable, ".",
	)
	if output, err := build.CombinedOutput(); err != nil {
		t.Fatalf("go build error = %v\n%s", err, output)
	}

	versionOutput, err := exec.Command(executable, "--version").CombinedOutput()
	if err != nil {
		t.Fatalf("clock --version error = %v\n%s", err, versionOutput)
	}
	if got := strings.TrimSpace(string(versionOutput)); got != "clock v1.2.3 (revision abc123)" {
		t.Errorf("clock --version = %q", got)
	}
	if strings.Contains(strings.ToLower(string(versionOutput)), "build time") {
		t.Errorf("clock --version contains a timestamp: %q", versionOutput)
	}

	helpOutput, err := exec.Command(executable, "--help").CombinedOutput()
	if err != nil {
		t.Fatalf("clock --help error = %v\n%s", err, helpOutput)
	}
	if !strings.Contains(string(helpOutput), "clock configure") {
		t.Errorf("clock --help = %q, want configure grammar", helpOutput)
	}
}

func TestCompiledClockCrashAfterConsumptionLeavesNoRetryState(t *testing.T) {
	t.Parallel()

	createStarted := make(chan struct{}, 1)
	releaseCreate := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		switch {
		case strings.HasSuffix(request.URL.Path, "/myself"):
			writer.Header().Set("Content-Type", "application/json")
			_, _ = writer.Write([]byte(`{"accountId":"account","displayName":"Clock Test","emailAddress":"person@example.com","active":true}`))
		case strings.HasSuffix(request.URL.Path, "/search/jql"):
			writer.Header().Set("Content-Type", "application/json")
			_, _ = writer.Write([]byte(`{"issues":[],"isLast":true}`))
		case strings.HasSuffix(request.URL.Path, "/worklog") && request.Method == http.MethodPost:
			createStarted <- struct{}{}
			<-releaseCreate
		default:
			http.Error(writer, "unexpected request", http.StatusNotFound)
		}
	}))
	defer server.Close()

	executable := filepath.Join(t.TempDir(), "clock")
	build := exec.Command("go", "build", "-tags", "clocktest", "-o", executable, ".")
	if output, err := build.CombinedOutput(); err != nil {
		t.Fatalf("go build error = %v\n%s", err, output)
	}
	configRoot := t.TempDir()
	now := time.Now().UTC()
	store := runningtimer.NewStore(configRoot)
	if err := store.Create(runningtimer.Timer{Issue: "CLOCK-14", StartedAt: now.Add(-time.Hour), CloudID: "cloud", AccountID: "account"}, now); err != nil {
		t.Fatal(err)
	}
	configText := `[jira]
site_url = "https://example.atlassian.net"
cloud_id = "cloud"
email = "person@example.com"
account_id = "account"

[earnings]
hourly_rate_czk = "750.00"
`
	if err := os.WriteFile(filepath.Join(configRoot, "clock", "config.toml"), []byte(configText), 0o600); err != nil {
		t.Fatal(err)
	}
	command := exec.Command(executable, "stop")
	command.Env = append(os.Environ(), "XDG_CONFIG_HOME="+configRoot, "TZ=UTC", "CLOCK_TEST_GATEWAY_URL="+server.URL)
	var commandOutput bytes.Buffer
	command.Stdout = &commandOutput
	command.Stderr = &commandOutput
	if err := command.Start(); err != nil {
		t.Fatal(err)
	}
	select {
	case <-createStarted:
	case <-time.After(10 * time.Second):
		_ = command.Process.Kill()
		close(releaseCreate)
		_ = command.Wait()
		t.Fatalf("clock did not reach Jira creation after consuming state: %s", commandOutput.String())
	}
	if err := command.Process.Kill(); err != nil {
		t.Fatal(err)
	}
	close(releaseCreate)
	_ = command.Wait()
	if _, err := store.Inspect(time.Now().UTC()); !errors.Is(err, runningtimer.ErrNoTimer) {
		t.Fatalf("state after compiled clock crash = %v, want no timer and no retry state", err)
	}
}

func TestCompiledBinaryRunsLocalTimerStatusAndDiscard(t *testing.T) {
	t.Parallel()

	executable := filepath.Join(t.TempDir(), "clock")
	build := exec.Command("go", "build", "-o", executable, ".")
	if output, err := build.CombinedOutput(); err != nil {
		t.Fatalf("go build error = %v\n%s", err, output)
	}
	configRoot := t.TempDir()
	now := time.Now().UTC()
	store := runningtimer.NewStore(configRoot)
	if err := store.Create(runningtimer.Timer{
		Issue: "CLOCK-14", StartedAt: now.Add(-time.Hour), Description: "Binary lifecycle",
		CloudID: "cloud", AccountID: "account",
	}, now); err != nil {
		t.Fatal(err)
	}
	configuration := `[jira]
site_url = "https://example.atlassian.net"
cloud_id = "cloud"
email = "person@example.com"
account_id = "account"

[earnings]
hourly_rate_czk = "750.00"
`
	if err := os.WriteFile(filepath.Join(configRoot, "clock", "config.toml"), []byte(configuration), 0o600); err != nil {
		t.Fatal(err)
	}
	environment := append(os.Environ(), "XDG_CONFIG_HOME="+configRoot, "TZ=UTC")
	status := exec.Command(executable, "status")
	status.Env = environment
	output, err := status.CombinedOutput()
	if err != nil || !strings.Contains(string(output), "CLOCK-14") || !strings.Contains(string(output), "Binary lifecycle") {
		t.Fatalf("clock status = %v\n%s", err, output)
	}
	discard := exec.Command(executable, "discard")
	discard.Env = environment
	output, err = discard.CombinedOutput()
	if err != nil || !strings.Contains(string(output), "No Jira Worklog was created") {
		t.Fatalf("clock discard = %v\n%s", err, output)
	}
}
