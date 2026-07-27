package log_test

import (
	"context"
	"errors"
	"testing"
	"time"

	applog "github.com/JirakLu/clock/internal/app/log"
	"github.com/JirakLu/clock/internal/app/recording"
	"github.com/JirakLu/clock/internal/config"
	"github.com/JirakLu/clock/internal/credential"
	"github.com/JirakLu/clock/internal/jiraidentity"
	"github.com/JirakLu/clock/internal/secret"
	"github.com/JirakLu/clock/internal/worklog"
)

func TestRunLoadsConfiguredIdentityAndCredentialThenRecords(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, time.July, 27, 12, 0, 0, 0, time.UTC)
	duration, _ := worklog.ParseCompactDuration("45m")
	configuration := configuredIdentity()
	token, _ := secret.NewToken("api-token")
	recorder := &fakeRecorder{result: recording.Result{Status: recording.Submitted}}
	service := applog.New(
		&fakeConfigurationLoader{configuration: configuration},
		&fakeCredentialStore{token: token},
		recorder,
		func() time.Time { return now },
	)

	result, err := service.Run(context.Background(), applog.Input{
		Issue: "CLOCK-13",
		Timing: recording.Timing{
			Mode: recording.EndingNow, Duration: duration,
		},
		Description: "Application test",
	})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if result.Status != recording.Submitted {
		t.Errorf("Run() result = %#v", result)
	}
	if recorder.auth.Identity != configuration.JiraIdentity ||
		recorder.auth.Token.Value() != token.Value() ||
		recorder.request.Issue != "CLOCK-13" ||
		recorder.request.Description != "Application test" ||
		!recorder.now.Equal(now) {
		t.Errorf("Record() arguments = auth %#v request %#v now %v", recorder.auth, recorder.request, recorder.now)
	}
	if got := recorder.request.Timing.Duration.Seconds(); got != 2700 {
		t.Errorf("Duration seconds = %d", got)
	}
}

func TestRunFailsClosedWhenConfigurationOrCredentialCannotLoad(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		configErr  error
		credential error
	}{
		{name: "malformed configuration", configErr: errors.New("parse /tmp/config.toml")},
		{name: "credential unavailable", credential: errors.New("native store locked")},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			recorder := &fakeRecorder{}
			service := applog.New(
				&fakeConfigurationLoader{
					configuration: configuredIdentity(),
					err:           test.configErr,
				},
				&fakeCredentialStore{err: test.credential},
				recorder,
				time.Now,
			)
			_, err := service.Run(context.Background(), applog.Input{Issue: "CLOCK-13"})
			if err == nil {
				t.Fatal("Run() unexpectedly succeeded")
			}
			if recorder.calls != 0 {
				t.Errorf("Record() called %d times", recorder.calls)
			}
		})
	}
}

func configuredIdentity() config.Configuration {
	return config.Configuration{JiraIdentity: jiraidentity.Reference{
		SiteURL: "https://example.atlassian.net", CloudID: "cloud",
		Email: "person@example.com", AccountID: "account",
	}}
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
	key   credential.IdentityKey
}

func (f *fakeCredentialStore) Get(key credential.IdentityKey) (secret.Token, error) {
	f.key = key
	return f.token, f.err
}

type fakeRecorder struct {
	result  recording.Result
	err     error
	auth    recording.Auth
	request recording.Request
	now     time.Time
	calls   int
}

func (f *fakeRecorder) Record(
	_ context.Context,
	auth recording.Auth,
	request recording.Request,
	now time.Time,
) (recording.Result, error) {
	f.calls++
	f.auth = auth
	f.request = request
	f.now = now
	return f.result, f.err
}
