package configure_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/JirakLu/clock/internal/app/configure"
	"github.com/JirakLu/clock/internal/config"
	"github.com/JirakLu/clock/internal/credential"
	"github.com/JirakLu/clock/internal/earnings"
	"github.com/JirakLu/clock/internal/jiraidentity"
	"github.com/JirakLu/clock/internal/secret"
)

func TestRunValidatesConfirmsAndCommitsConfiguration(t *testing.T) {
	t.Parallel()

	rate, _ := earnings.ParseHourlyRate("750.00")
	token, _ := secret.NewToken("api-token")
	identity := jiraidentity.Identity{
		Reference: jiraidentity.Reference{
			SiteURL: "https://example.atlassian.net", CloudID: "cloud",
			Email: "person@example.com", AccountID: "account",
		},
		DisplayName: "Example Person",
	}
	var events []string
	validator := &fakeValidator{identity: identity, events: &events}
	confirmer := &fakeConfirmer{confirmed: true, events: &events}
	credentials := newFakeCredentials(&events)
	configurations := config.NewStore(t.TempDir())
	service := configure.New(validator, confirmer, credentials, configurations)

	result, err := service.Run(context.Background(), configure.Input{
		SiteURL: identity.SiteURL, Email: identity.Email, HourlyRate: rate, Token: token,
	})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if result.Identity != identity || result.HourlyRate != rate {
		t.Errorf("Run() result = %#v", result)
	}
	wantEvents := []string{"validate", "confirm", "credential-get", "credential-set"}
	if !reflect.DeepEqual(events, wantEvents) {
		t.Errorf("events = %v, want %v", events, wantEvents)
	}
	saved, err := configurations.Load()
	if err != nil {
		t.Fatalf("Load() saved configuration error = %v", err)
	}
	if saved.JiraIdentity.AccountID != identity.AccountID {
		t.Errorf("saved configuration = %#v", saved)
	}
	if resultText := result.Identity.DisplayName; resultText == token.Value() {
		t.Fatal("result exposed token")
	}
}

func TestRunDeclinedConfirmationDoesNotMutatePersistence(t *testing.T) {
	t.Parallel()

	rate, _ := earnings.ParseHourlyRate("750")
	token, _ := secret.NewToken("api-token")
	events := []string{}
	service := configure.New(
		&fakeValidator{identity: validIdentity(), events: &events},
		&fakeConfirmer{confirmed: false, events: &events},
		newFakeCredentials(&events),
		config.NewStore(t.TempDir()),
	)

	_, err := service.Run(context.Background(), configure.Input{
		SiteURL: "https://example.atlassian.net", Email: "person@example.com",
		HourlyRate: rate, Token: token,
	})
	if !errors.Is(err, configure.ErrDeclined) {
		t.Fatalf("Run() error = %v, want ErrDeclined", err)
	}
	if want := []string{"validate", "confirm"}; !reflect.DeepEqual(events, want) {
		t.Errorf("events = %v, want %v", events, want)
	}
}

func TestRunKeyringFailureLeavesConfigurationUntouched(t *testing.T) {
	t.Parallel()

	rate, _ := earnings.ParseHourlyRate("750")
	token, _ := secret.NewToken("api-token")
	events := []string{}
	credentials := newFakeCredentials(&events)
	credentials.setErr = errors.New("keyring locked")
	configurations := config.NewStore(t.TempDir())
	service := configure.New(
		&fakeValidator{identity: validIdentity(), events: &events},
		&fakeConfirmer{confirmed: true, events: &events},
		credentials,
		configurations,
	)

	if _, err := service.Run(context.Background(), configure.Input{
		SiteURL: "https://example.atlassian.net", Email: "person@example.com",
		HourlyRate: rate, Token: token,
	}); err == nil {
		t.Fatal("Run() unexpectedly succeeded")
	}
	if _, err := configurations.Load(); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("configuration exists after keyring failure: %v", err)
	}
}

func TestRunPersistenceFailureRestoresPreviousCredential(t *testing.T) {
	rate, _ := earnings.ParseHourlyRate("750")
	oldToken, _ := secret.NewToken("working-token")
	newToken, _ := secret.NewToken("replacement-token")
	events := []string{}
	credentials := newFakeCredentials(&events)
	credentials.token = oldToken
	blockedRoot := filepath.Join(t.TempDir(), "not-a-directory")
	if err := os.WriteFile(blockedRoot, []byte("blocks configuration directory"), 0o600); err != nil {
		t.Fatal(err)
	}
	configurations := config.NewStore(blockedRoot)
	service := configure.New(
		&fakeValidator{identity: validIdentity(), events: &events},
		&fakeConfirmer{confirmed: true, events: &events},
		credentials,
		configurations,
	)

	if _, err := service.Run(context.Background(), configure.Input{
		SiteURL: "https://example.atlassian.net", Email: "person@example.com",
		HourlyRate: rate, Token: newToken,
	}); err == nil {
		t.Fatal("Run() unexpectedly succeeded")
	}
	if credentials.token.Value() != oldToken.Value() {
		t.Fatal("configuration failure did not restore the working credential")
	}
	wantEvents := []string{
		"validate", "confirm", "credential-get", "credential-set", "credential-set",
	}
	if !reflect.DeepEqual(events, wantEvents) {
		t.Errorf("events = %v, want %v", events, wantEvents)
	}
}

func TestRunWithoutNewTokenRetainsExistingCredential(t *testing.T) {
	t.Parallel()

	rate, _ := earnings.ParseHourlyRate("800")
	existingToken, _ := secret.NewToken("working-token")
	existing := config.Configuration{
		JiraIdentity: jiraidentity.Reference{
			SiteURL: "https://example.atlassian.net", CloudID: "cloud",
			Email: "person@example.com", AccountID: "account",
		},
		HourlyRate: rate,
	}
	events := []string{}
	credentials := newFakeCredentials(&events)
	credentials.token = existingToken
	configurations := config.NewStore(t.TempDir())
	if err := configurations.Save(existing); err != nil {
		t.Fatal(err)
	}
	service := configure.New(
		&fakeValidator{identity: validIdentity(), events: &events},
		&fakeConfirmer{confirmed: true, events: &events},
		credentials,
		configurations,
	)

	if _, err := service.Run(context.Background(), configure.Input{
		SiteURL: existing.JiraIdentity.SiteURL,
		Email:   existing.JiraIdentity.Email, HourlyRate: rate,
	}); err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if credentials.token.Value() != existingToken.Value() {
		t.Fatal("Run() did not retain existing token")
	}
}

func validIdentity() jiraidentity.Identity {
	return jiraidentity.Identity{
		Reference: jiraidentity.Reference{
			SiteURL: "https://example.atlassian.net", CloudID: "cloud",
			Email: "person@example.com", AccountID: "account",
		},
		DisplayName: "Example Person",
	}
}

type fakeValidator struct {
	identity jiraidentity.Identity
	err      error
	events   *[]string
}

func (f *fakeValidator) DiscoverAndValidate(
	_ context.Context, _ string, _ string, _ secret.Token,
) (jiraidentity.Identity, error) {
	*f.events = append(*f.events, "validate")
	return f.identity, f.err
}

type fakeConfirmer struct {
	confirmed bool
	err       error
	events    *[]string
}

func (f *fakeConfirmer) Confirm(_ context.Context, _ configure.Proposal) (bool, error) {
	*f.events = append(*f.events, "confirm")
	return f.confirmed, f.err
}

type fakeCredentials struct {
	token  secret.Token
	getErr error
	setErr error
	events *[]string
}

func newFakeCredentials(events *[]string) *fakeCredentials {
	return &fakeCredentials{getErr: credential.ErrNotFound, events: events}
}

func (f *fakeCredentials) Get(credential.IdentityKey) (secret.Token, error) {
	*f.events = append(*f.events, "credential-get")
	if !f.token.Empty() {
		return f.token, nil
	}
	return secret.Token{}, f.getErr
}

func (f *fakeCredentials) Set(_ credential.IdentityKey, token secret.Token) error {
	*f.events = append(*f.events, "credential-set")
	if f.setErr == nil {
		f.token = token
	}
	return f.setErr
}

func (f *fakeCredentials) Delete(credential.IdentityKey) error {
	*f.events = append(*f.events, "credential-delete")
	f.token = secret.Token{}
	return nil
}
