package report

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/JirakLu/clock/internal/app/recording"
	"github.com/JirakLu/clock/internal/config"
	"github.com/JirakLu/clock/internal/credential"
	"github.com/JirakLu/clock/internal/earnings"
	domainreport "github.com/JirakLu/clock/internal/report"
	"github.com/JirakLu/clock/internal/secret"
	"github.com/JirakLu/clock/internal/worklog"
)

type Input struct {
	Selector domainreport.Selector
	From     time.Time
	To       time.Time
	Earnings bool
}

type Result struct {
	Report          domainreport.Report
	IncludeEarnings bool
	HourlyRate      earnings.HourlyRate
}

type ConfigurationLoader interface {
	Load() (config.Configuration, error)
}

type CredentialStore interface {
	Get(credential.IdentityKey) (secret.Token, error)
}

type Gateway interface {
	VerifyIdentity(context.Context, recording.Auth) error
	ListAuthoredWorklogs(
		context.Context,
		recording.Auth,
		time.Time,
		time.Time,
	) ([]worklog.Worklog, error)
}

type Service struct {
	configurations ConfigurationLoader
	credentials    CredentialStore
	gateway        Gateway
	now            func() time.Time
	location       *time.Location
}

func New(
	configurations ConfigurationLoader,
	credentials CredentialStore,
	gateway Gateway,
	now func() time.Time,
	location *time.Location,
) *Service {
	if now == nil {
		now = time.Now
	}
	if location == nil {
		location = time.Local
	}
	return &Service{
		configurations: configurations,
		credentials:    credentials,
		gateway:        gateway,
		now:            now,
		location:       location,
	}
}

func (s *Service) Run(ctx context.Context, input Input) (Result, error) {
	if s.configurations == nil || s.credentials == nil || s.gateway == nil {
		return Result{}, errors.New("Worklog reporting is unavailable")
	}
	window, err := s.resolveWindow(input)
	if err != nil {
		return Result{}, err
	}
	configuration, err := s.configurations.Load()
	if err != nil {
		return Result{}, fmt.Errorf("load Jira configuration: %w", err)
	}
	token, err := s.credentials.Get(credential.IdentityKey{
		CloudID:   configuration.JiraIdentity.CloudID,
		AccountID: configuration.JiraIdentity.AccountID,
	})
	if err != nil {
		return Result{}, fmt.Errorf("load Jira API token from native credential store: %w", err)
	}
	auth := recording.Auth{Identity: configuration.JiraIdentity, Token: token}
	if err := s.gateway.VerifyIdentity(ctx, auth); err != nil {
		return Result{}, fmt.Errorf("verify configured Jira identity: %w", err)
	}
	worklogs, err := s.gateway.ListAuthoredWorklogs(ctx, auth, window.From, window.To)
	if err != nil {
		return Result{}, fmt.Errorf("read accessible authored Worklogs: %w", err)
	}
	return Result{
		Report:          domainreport.Build(window, worklogs),
		IncludeEarnings: input.Earnings,
		HourlyRate:      configuration.HourlyRate,
	}, nil
}

func (s *Service) resolveWindow(input Input) (domainreport.Window, error) {
	if input.Selector == domainreport.Explicit {
		return domainreport.NewWindow(input.Selector, input.From, input.To, s.location)
	}
	return domainreport.ResolvePreset(input.Selector, s.now(), s.location)
}
