package log

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/JirakLu/clock/internal/app/recording"
	"github.com/JirakLu/clock/internal/config"
	"github.com/JirakLu/clock/internal/credential"
	"github.com/JirakLu/clock/internal/secret"
	"github.com/JirakLu/clock/internal/worklog"
)

type Input struct {
	Issue       worklog.IssueKey
	Timing      recording.Timing
	Description string
}

type Result = recording.Result

type ConfigurationLoader interface {
	Load() (config.Configuration, error)
}

type CredentialStore interface {
	Get(credential.IdentityKey) (secret.Token, error)
}

type Recorder interface {
	Record(
		context.Context,
		recording.Auth,
		recording.Request,
		time.Time,
	) (recording.Result, error)
}

type Service struct {
	configurations ConfigurationLoader
	credentials    CredentialStore
	recorder       Recorder
	now            func() time.Time
}

func New(
	configurations ConfigurationLoader,
	credentials CredentialStore,
	recorder Recorder,
	now func() time.Time,
) *Service {
	if now == nil {
		now = time.Now
	}
	return &Service{
		configurations: configurations,
		credentials:    credentials,
		recorder:       recorder,
		now:            now,
	}
}

func (s *Service) Run(ctx context.Context, input Input) (Result, error) {
	if s.configurations == nil || s.credentials == nil || s.recorder == nil {
		return Result{}, errors.New("completed Worklog logging is unavailable")
	}
	configuration, err := s.configurations.Load()
	if err != nil {
		return Result{}, fmt.Errorf("load Jira configuration: %w", err)
	}
	key := credential.IdentityKey{
		CloudID:   configuration.JiraIdentity.CloudID,
		AccountID: configuration.JiraIdentity.AccountID,
	}
	token, err := s.credentials.Get(key)
	if err != nil {
		return Result{}, fmt.Errorf("load Jira API token from native credential store: %w", err)
	}
	return s.recorder.Record(
		ctx,
		recording.Auth{Identity: configuration.JiraIdentity, Token: token},
		recording.Request{
			Issue: input.Issue, Timing: input.Timing, Description: input.Description,
		},
		s.now(),
	)
}
