package cli_test

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"testing"

	appconfigure "github.com/JirakLu/clock/internal/app/configure"
	"github.com/JirakLu/clock/internal/cli"
	"github.com/JirakLu/clock/internal/jiraidentity"
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
			want: []string{"clock configure", "secure native credential store", "--version"},
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
