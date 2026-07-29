package report_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/JirakLu/clock/internal/app/recording"
	appreport "github.com/JirakLu/clock/internal/app/report"
	"github.com/JirakLu/clock/internal/config"
	"github.com/JirakLu/clock/internal/credential"
	"github.com/JirakLu/clock/internal/earnings"
	"github.com/JirakLu/clock/internal/jiraidentity"
	"github.com/JirakLu/clock/internal/report"
	"github.com/JirakLu/clock/internal/secret"
	"github.com/JirakLu/clock/internal/worklog"
)

func TestRunAuthenticatesReadsAndBuildsReport(t *testing.T) {
	t.Parallel()

	prague, _ := time.LoadLocation("Europe/Prague")
	now := time.Date(2026, time.July, 29, 12, 0, 0, 0, prague)
	rate, _ := earnings.ParseHourlyRate("750.00")
	configuration := config.Configuration{
		JiraIdentity: jiraidentity.Reference{
			SiteURL: "https://example.atlassian.net", CloudID: "cloud",
			Email: "person@example.com", AccountID: "account",
		},
		HourlyRate: rate,
	}
	token, _ := secret.NewToken("token")
	interval, _ := worklog.NewInterval(
		time.Date(2026, time.July, 29, 9, 0, 0, 0, prague),
		time.Date(2026, time.July, 29, 10, 0, 0, 0, prague),
	)
	gateway := &fakeGateway{worklogs: []worklog.Worklog{{
		ID: "1", Issue: "CLOCK-16", Summary: "Reports", AuthorID: "account", Interval: interval,
	}}}
	service := appreport.New(
		&fakeConfigurationLoader{configuration: configuration},
		&fakeCredentialStore{token: token},
		gateway,
		func() time.Time { return now },
		prague,
	)

	result, err := service.Run(context.Background(), appreport.Input{
		Selector: report.Today, Earnings: true,
	})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if gateway.verifyCalls != 1 || gateway.listCalls != 1 {
		t.Errorf("gateway calls = verify %d list %d", gateway.verifyCalls, gateway.listCalls)
	}
	if gateway.auth.Identity != configuration.JiraIdentity ||
		gateway.auth.Token.Value() != "token" {
		t.Errorf("auth = %#v", gateway.auth)
	}
	if result.Report.TotalSeconds != 3600 || result.HourlyRate.QuotedCZK() != "750.00" ||
		!result.IncludeEarnings {
		t.Errorf("result = %#v", result)
	}
}

func TestRunFailsClosedBeforeReadWhenIdentityVerificationFails(t *testing.T) {
	t.Parallel()

	prague, _ := time.LoadLocation("Europe/Prague")
	rate, _ := earnings.ParseHourlyRate("750")
	token, _ := secret.NewToken("token")
	gateway := &fakeGateway{verifyErr: errors.New("identity mismatch")}
	service := appreport.New(
		&fakeConfigurationLoader{configuration: config.Configuration{
			JiraIdentity: jiraidentity.Reference{
				SiteURL: "https://example.atlassian.net", CloudID: "cloud",
				Email: "person@example.com", AccountID: "account",
			},
			HourlyRate: rate,
		}},
		&fakeCredentialStore{token: token},
		gateway,
		time.Now,
		prague,
	)

	_, err := service.Run(context.Background(), appreport.Input{Selector: report.Today})
	if err == nil {
		t.Fatal("Run() unexpectedly succeeded")
	}
	if gateway.listCalls != 0 {
		t.Errorf("ListAuthoredWorklogs() called %d times", gateway.listCalls)
	}
}

type fakeConfigurationLoader struct {
	configuration config.Configuration
	err           error
}

func (f *fakeConfigurationLoader) Load() (config.Configuration, error) {
	return f.configuration, f.err
}

type fakeCredentialStore struct {
	token secret.Token
	err   error
}

func (f *fakeCredentialStore) Get(credential.IdentityKey) (secret.Token, error) {
	return f.token, f.err
}

type fakeGateway struct {
	worklogs    []worklog.Worklog
	verifyErr   error
	listErr     error
	auth        recording.Auth
	from        time.Time
	to          time.Time
	verifyCalls int
	listCalls   int
}

func (f *fakeGateway) VerifyIdentity(_ context.Context, auth recording.Auth) error {
	f.verifyCalls++
	f.auth = auth
	return f.verifyErr
}

func (f *fakeGateway) ListAuthoredWorklogs(
	_ context.Context,
	auth recording.Auth,
	from time.Time,
	to time.Time,
) ([]worklog.Worklog, error) {
	f.listCalls++
	f.auth, f.from, f.to = auth, from, to
	return f.worklogs, f.listErr
}
