package cli_test

import (
	"bytes"
	"context"
	"strings"
	"testing"

	appconfigure "github.com/JirakLu/clock/internal/app/configure"
	"github.com/JirakLu/clock/internal/cli"
	"github.com/JirakLu/clock/internal/earnings"
	"github.com/JirakLu/clock/internal/jiraidentity"
)

func TestInteractivePrompterHidesTokenAndConfirmsIdentity(t *testing.T) {
	t.Parallel()

	const tokenRaw = "hidden-token"
	var output bytes.Buffer
	secretReader := &fakeSecretReader{value: tokenRaw}
	prompter := cli.NewInteractivePrompter(
		strings.NewReader("yes\n"),
		&output,
		secretReader,
	)
	rate, _ := earnings.ParseHourlyRate("750")
	proposal := appconfigure.Proposal{
		Identity: jiraidentity.Identity{
			Reference: jiraidentity.Reference{
				SiteURL: "https://example.atlassian.net", CloudID: "cloud",
				Email: "person@example.com", AccountID: "account",
			},
			DisplayName: "Example Person",
		},
		HourlyRate: rate,
	}

	gotSecret, err := prompter.ReadSecret("API token")
	if err != nil {
		t.Fatal(err)
	}
	if gotSecret != tokenRaw {
		t.Fatal("ReadSecret() did not return the secret reader's value")
	}
	confirmed, err := prompter.Confirm(context.Background(), proposal)
	if err != nil {
		t.Fatal(err)
	}
	if !confirmed {
		t.Fatal("Confirm() = false, want true")
	}
	if strings.Contains(output.String(), tokenRaw) {
		t.Fatalf("prompt output exposed token: %q", output.String())
	}
	for _, expected := range []string{"Example Person", "account", "https://example.atlassian.net"} {
		if !strings.Contains(output.String(), expected) {
			t.Errorf("prompt output = %q, want %q", output.String(), expected)
		}
	}
}

type fakeSecretReader struct {
	value string
}

func (f *fakeSecretReader) ReadSecret() (string, error) {
	return f.value, nil
}
