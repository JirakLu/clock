package credential

import (
	"encoding/base64"
	"errors"
	"fmt"
	"strings"

	"github.com/JirakLu/clock/internal/jiraidentity"
	"github.com/JirakLu/clock/internal/secret"
	"github.com/zalando/go-keyring"
)

const serviceName = "clock"

var ErrNotFound = errors.New("credential not found")

type IdentityKey struct {
	CloudID   jiraidentity.CloudID
	AccountID jiraidentity.AccountID
}

func (k IdentityKey) AccountName() string {
	encode := base64.RawURLEncoding.EncodeToString
	return "jira:" + encode([]byte(k.CloudID)) + "." + encode([]byte(k.AccountID))
}

func (k IdentityKey) validate() error {
	if strings.TrimSpace(string(k.CloudID)) == "" || strings.TrimSpace(string(k.AccountID)) == "" {
		return errors.New("credential identity requires both Jira Cloud ID and account ID")
	}
	return nil
}

type NativeStore struct{}

func NewNativeStore() *NativeStore {
	return &NativeStore{}
}

func (s *NativeStore) Get(identity IdentityKey) (secret.Token, error) {
	if err := identity.validate(); err != nil {
		return secret.Token{}, err
	}
	value, err := keyring.Get(serviceName, identity.AccountName())
	if errors.Is(err, keyring.ErrNotFound) {
		return secret.Token{}, ErrNotFound
	}
	if err != nil {
		return secret.Token{}, unavailableError("read", err)
	}
	token, ok := secret.NewToken(value)
	if !ok {
		return secret.Token{}, errors.New("native credential store returned an empty Jira API token")
	}
	return token, nil
}

func (s *NativeStore) Set(identity IdentityKey, token secret.Token) error {
	if err := identity.validate(); err != nil {
		return err
	}
	if token.Empty() {
		return errors.New("refusing to store an empty Jira API token")
	}
	if err := keyring.Set(serviceName, identity.AccountName(), token.Value()); err != nil {
		return unavailableError("write", err)
	}
	return nil
}

func (s *NativeStore) Delete(identity IdentityKey) error {
	if err := identity.validate(); err != nil {
		return err
	}
	if err := keyring.Delete(serviceName, identity.AccountName()); err != nil {
		if errors.Is(err, keyring.ErrNotFound) {
			return ErrNotFound
		}
		return unavailableError("delete", err)
	}
	return nil
}

func unavailableError(operation string, cause error) error {
	return fmt.Errorf(
		"%s Jira API token in the native credential store: %w; unlock or start macOS Keychain or Linux Secret Service and try again",
		operation,
		cause,
	)
}
