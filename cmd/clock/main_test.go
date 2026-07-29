package main

import (
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
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
