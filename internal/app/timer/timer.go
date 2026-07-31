package timer

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/JirakLu/clock/internal/app/recording"
	"github.com/JirakLu/clock/internal/config"
	"github.com/JirakLu/clock/internal/credential"
	"github.com/JirakLu/clock/internal/runningtimer"
	"github.com/JirakLu/clock/internal/secret"
	"github.com/JirakLu/clock/internal/worklog"
)

type StartInput struct {
	Issue         worklog.IssueKey
	Mode          recording.TimingMode
	ExplicitStart time.Time
	Description   string
}

type StartResult struct {
	Timer runningtimer.Timer
}

type StatusResult struct {
	Active           bool
	Timer            runningtimer.Timer
	ElapsedSeconds   int64
	IdentityMismatch bool
}

type StopInput struct {
	StopAt              time.Time
	Description         string
	DescriptionOverride bool
}

type StopResult = recording.Result

type DiscardResult struct {
	Timer runningtimer.Timer
}

type AlreadyRunningError struct {
	Timer runningtimer.Timer
}

func (e *AlreadyRunningError) Error() string { return "a Running timer is already active" }

type ConfigurationLoader interface {
	Load() (config.Configuration, error)
}

type CredentialStore interface {
	Get(credential.IdentityKey) (secret.Token, error)
}

type StateStore interface {
	Inspect(time.Time) (runningtimer.Inspection, error)
	Create(runningtimer.Timer, time.Time) error
	Consume(runningtimer.Timer, time.Time) error
	Discard(runningtimer.Timer, time.Time) error
}

type Recorder interface {
	ResolveTimerStart(context.Context, recording.Auth, recording.TimingMode, time.Time, time.Time) (time.Time, error)
	Prepare(context.Context, recording.Auth, recording.Request, time.Time) (worklog.Draft, error)
	Submit(context.Context, recording.Auth, worklog.Draft) recording.Result
}

type Service struct {
	configurations ConfigurationLoader
	credentials    CredentialStore
	state          StateStore
	recorder       Recorder
	now            func() time.Time
}

func New(configurations ConfigurationLoader, credentials CredentialStore, state StateStore, recorder Recorder, now func() time.Time) *Service {
	if now == nil {
		now = time.Now
	}
	return &Service{configurations: configurations, credentials: credentials, state: state, recorder: recorder, now: now}
}

func (s *Service) Start(ctx context.Context, input StartInput) (StartResult, error) {
	if s.state == nil || s.recorder == nil {
		return StartResult{}, errors.New("Running timer capability is unavailable")
	}
	now := s.now()
	inspection, err := s.state.Inspect(now)
	if err == nil {
		return StartResult{}, &AlreadyRunningError{Timer: inspection.Timer}
	}
	if !errors.Is(err, runningtimer.ErrNoTimer) {
		return StartResult{}, err
	}
	auth, err := s.loadAuth()
	if err != nil {
		return StartResult{}, err
	}
	startedAt, err := s.recorder.ResolveTimerStart(ctx, auth, input.Mode, input.ExplicitStart, now)
	if err != nil {
		return StartResult{}, err
	}
	timer := runningtimer.Timer{
		Issue: input.Issue, StartedAt: startedAt, Description: input.Description,
		CloudID: auth.Identity.CloudID, AccountID: auth.Identity.AccountID,
	}
	if err := s.state.Create(timer, now); err != nil {
		// Another process may have won the create race after our first
		// inspection. Re-read so the caller still receives the active timer and
		// the required stop/discard guidance instead of a generic filesystem error.
		if active, inspectErr := s.state.Inspect(now); inspectErr == nil {
			return StartResult{}, &AlreadyRunningError{Timer: active.Timer}
		}
		return StartResult{}, fmt.Errorf("create Running timer: %w", err)
	}
	return StartResult{Timer: timer}, nil
}

func (s *Service) Status() (StatusResult, error) {
	if s.state == nil {
		return StatusResult{}, errors.New("Running timer capability is unavailable")
	}
	now := s.now()
	inspection, err := s.state.Inspect(now)
	if errors.Is(err, runningtimer.ErrNoTimer) {
		return StatusResult{}, nil
	}
	if err != nil {
		return StatusResult{}, err
	}
	if s.configurations == nil {
		return StatusResult{}, errors.New("Jira configuration capability is unavailable")
	}
	configuration, err := s.configurations.Load()
	if err != nil {
		return StatusResult{}, fmt.Errorf("load Jira configuration: %w", err)
	}
	elapsed := int64(now.Sub(inspection.Timer.StartedAt) / time.Second)
	if elapsed < 0 {
		elapsed = 0
	}
	return StatusResult{
		Active: true, Timer: inspection.Timer, ElapsedSeconds: elapsed,
		IdentityMismatch: inspection.Timer.CloudID != configuration.JiraIdentity.CloudID ||
			inspection.Timer.AccountID != configuration.JiraIdentity.AccountID,
	}, nil
}

func (s *Service) Stop(ctx context.Context, input StopInput) (StopResult, error) {
	if s.state == nil || s.recorder == nil {
		return StopResult{}, errors.New("Running timer capability is unavailable")
	}
	now := s.now()
	inspection, err := s.state.Inspect(now)
	if err != nil {
		return StopResult{}, err
	}
	stopAt := input.StopAt
	if stopAt.IsZero() {
		stopAt = now
	}
	if stopAt.After(now) {
		return StopResult{}, errors.New("Running timer stop must not be in the future")
	}
	seconds := int64(stopAt.Sub(inspection.Timer.StartedAt) / time.Second)
	duration, err := worklog.DurationFromSeconds(seconds)
	if err != nil {
		return StopResult{}, recording.ErrNonPositiveWorklog
	}
	configuration, err := s.loadConfiguration()
	if err != nil {
		return StopResult{}, err
	}
	if inspection.Timer.CloudID != configuration.JiraIdentity.CloudID || inspection.Timer.AccountID != configuration.JiraIdentity.AccountID {
		return StopResult{}, errors.New("Running timer belongs to a different Jira Cloud ID or account ID; discard it or restore the original configuration")
	}
	auth, err := s.authFor(configuration)
	if err != nil {
		return StopResult{}, err
	}
	description := inspection.Timer.Description
	if input.DescriptionOverride {
		description = input.Description
	}
	draft, err := s.recorder.Prepare(ctx, auth, recording.Request{
		Issue:       inspection.Timer.Issue,
		Timing:      recording.Timing{Mode: recording.AtStart, Start: inspection.Timer.StartedAt, Duration: duration},
		Description: description,
	}, now)
	if err != nil {
		return StopResult{}, err
	}
	if err := s.state.Consume(inspection.Timer, now); err != nil {
		return StopResult{}, fmt.Errorf("consume Running timer before Jira submission: %w", err)
	}
	result := s.recorder.Submit(ctx, auth, draft)
	result.Attempt = draft
	return result, nil
}

func (s *Service) Discard() (DiscardResult, error) {
	if s.state == nil {
		return DiscardResult{}, errors.New("Running timer capability is unavailable")
	}
	now := s.now()
	inspection, err := s.state.Inspect(now)
	if err != nil {
		return DiscardResult{}, err
	}
	if err := s.state.Discard(inspection.Timer, now); err != nil {
		return DiscardResult{}, err
	}
	return DiscardResult{Timer: inspection.Timer}, nil
}

func (s *Service) loadAuth() (recording.Auth, error) {
	configuration, err := s.loadConfiguration()
	if err != nil {
		return recording.Auth{}, err
	}
	return s.authFor(configuration)
}

func (s *Service) loadConfiguration() (config.Configuration, error) {
	if s.configurations == nil || s.credentials == nil {
		return config.Configuration{}, errors.New("Jira authentication capability is unavailable")
	}
	configuration, err := s.configurations.Load()
	if err != nil {
		return config.Configuration{}, fmt.Errorf("load Jira configuration: %w", err)
	}
	return configuration, nil
}

func (s *Service) authFor(configuration config.Configuration) (recording.Auth, error) {
	key := credential.IdentityKey{CloudID: configuration.JiraIdentity.CloudID, AccountID: configuration.JiraIdentity.AccountID}
	token, err := s.credentials.Get(key)
	if err != nil {
		return recording.Auth{}, fmt.Errorf("load Jira API token from native credential store: %w", err)
	}
	return recording.Auth{Identity: configuration.JiraIdentity, Token: token}, nil
}
