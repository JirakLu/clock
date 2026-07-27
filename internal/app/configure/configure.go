package configure

import (
	"context"
	"errors"
	"fmt"
	"os"

	"github.com/JirakLu/clock/internal/config"
	"github.com/JirakLu/clock/internal/credential"
	"github.com/JirakLu/clock/internal/earnings"
	"github.com/JirakLu/clock/internal/jiraidentity"
	"github.com/JirakLu/clock/internal/secret"
)

var ErrDeclined = errors.New("configuration was not confirmed")

type Input struct {
	SiteURL    string
	Email      string
	HourlyRate earnings.HourlyRate
	Token      secret.Token
}

type Proposal struct {
	Identity   jiraidentity.Identity
	HourlyRate earnings.HourlyRate
}

type Result struct {
	Identity   jiraidentity.Identity
	HourlyRate earnings.HourlyRate
}

type IdentityValidator interface {
	DiscoverAndValidate(context.Context, string, string, secret.Token) (jiraidentity.Identity, error)
}

type Confirmer interface {
	Confirm(context.Context, Proposal) (bool, error)
}

type CredentialStore interface {
	Get(credential.IdentityKey) (secret.Token, error)
	Set(credential.IdentityKey, secret.Token) error
	Delete(credential.IdentityKey) error
}

type Service struct {
	validator      IdentityValidator
	confirmer      Confirmer
	credentials    CredentialStore
	configurations *config.Store
}

func New(
	validator IdentityValidator,
	confirmer Confirmer,
	credentials CredentialStore,
	configurations *config.Store,
) *Service {
	return &Service{
		validator: validator, confirmer: confirmer,
		credentials: credentials, configurations: configurations,
	}
}

func (s *Service) Run(ctx context.Context, input Input) (Result, error) {
	if !input.HourlyRate.Valid() {
		return Result{}, errors.New("Hourly rate must be a valid non-negative CZK amount")
	}
	token, err := s.resolveToken(input)
	if err != nil {
		return Result{}, err
	}

	identity, err := s.validator.DiscoverAndValidate(ctx, input.SiteURL, input.Email, token)
	if err != nil {
		return Result{}, fmt.Errorf("validate proposed Jira configuration: %w", err)
	}
	proposal := Proposal{Identity: identity, HourlyRate: input.HourlyRate}
	confirmed, err := s.confirmer.Confirm(ctx, proposal)
	if err != nil {
		return Result{}, fmt.Errorf("confirm proposed Jira configuration: %w", err)
	}
	if !confirmed {
		return Result{}, ErrDeclined
	}

	key := credential.IdentityKey{CloudID: identity.CloudID, AccountID: identity.AccountID}
	previousToken, getErr := s.credentials.Get(key)
	if getErr != nil && !errors.Is(getErr, credential.ErrNotFound) {
		return Result{}, fmt.Errorf("prepare Jira credential update: %w", getErr)
	}
	hadPreviousToken := getErr == nil
	if err := s.credentials.Set(key, token); err != nil {
		return Result{}, fmt.Errorf("store Jira credential: %w", err)
	}

	configuration := config.Configuration{
		JiraIdentity: identity.Reference,
		HourlyRate:   input.HourlyRate,
	}
	if err := s.configurations.Save(configuration); err != nil {
		rollbackErr := s.rollbackCredential(key, previousToken, hadPreviousToken)
		if rollbackErr != nil {
			return Result{}, fmt.Errorf(
				"save validated configuration: %w; additionally failed to restore the previous credential: %v",
				err,
				rollbackErr,
			)
		}
		return Result{}, fmt.Errorf("save validated configuration: %w", err)
	}

	return Result{Identity: identity, HourlyRate: input.HourlyRate}, nil
}

func (s *Service) resolveToken(input Input) (secret.Token, error) {
	if !input.Token.Empty() {
		return input.Token, nil
	}

	existing, err := s.configurations.Load()
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return secret.Token{}, errors.New("API token is required for first-time configuration")
		}
		return secret.Token{}, fmt.Errorf(
			"cannot retain the existing API token because configuration could not be loaded: %w; enter a replacement token",
			err,
		)
	}
	if canonicalSite, err := config.CanonicalSiteURL(input.SiteURL); err != nil ||
		canonicalSite != existing.JiraIdentity.SiteURL ||
		input.Email != existing.JiraIdentity.Email {
		return secret.Token{}, errors.New(
			"API token is required when changing the Jira site or Atlassian email",
		)
	}
	key := credential.IdentityKey{
		CloudID: existing.JiraIdentity.CloudID, AccountID: existing.JiraIdentity.AccountID,
	}
	token, err := s.credentials.Get(key)
	if err != nil {
		return secret.Token{}, fmt.Errorf("retain existing Jira API token: %w", err)
	}
	return token, nil
}

func (s *Service) rollbackCredential(
	key credential.IdentityKey,
	previousToken secret.Token,
	hadPreviousToken bool,
) error {
	if hadPreviousToken {
		return s.credentials.Set(key, previousToken)
	}
	err := s.credentials.Delete(key)
	if errors.Is(err, credential.ErrNotFound) {
		return nil
	}
	return err
}
