package credential_test

import (
	"errors"
	"strings"
	"testing"

	"github.com/JirakLu/clock/internal/credential"
	"github.com/JirakLu/clock/internal/secret"
	"github.com/zalando/go-keyring"
)

func TestNativeStoreContract(t *testing.T) {
	keyring.MockInit()
	store := credential.NewNativeStore()
	identity := credential.IdentityKey{CloudID: "cloud-123", AccountID: "account-456"}
	token, _ := secret.NewToken("api-token-value")

	if _, err := store.Get(identity); !errors.Is(err, credential.ErrNotFound) {
		t.Fatalf("Get() before Set error = %v, want ErrNotFound", err)
	}
	if err := store.Set(identity, token); err != nil {
		t.Fatalf("Set() error = %v", err)
	}
	got, err := store.Get(identity)
	if err != nil {
		t.Fatalf("Get() error = %v", err)
	}
	if got.Value() != token.Value() {
		t.Errorf("Get() token did not round trip")
	}
	if err := store.Delete(identity); err != nil {
		t.Fatalf("Delete() error = %v", err)
	}
	if _, err := store.Get(identity); !errors.Is(err, credential.ErrNotFound) {
		t.Fatalf("Get() after Delete error = %v, want ErrNotFound", err)
	}
}

func TestIdentityKeyBindsCloudAndAccount(t *testing.T) {
	t.Parallel()

	first := credential.IdentityKey{CloudID: "cloud-a", AccountID: "account"}
	second := credential.IdentityKey{CloudID: "cloud-b", AccountID: "account"}
	third := credential.IdentityKey{CloudID: "cloud-a", AccountID: "other"}

	if first.AccountName() == second.AccountName() || first.AccountName() == third.AccountName() {
		t.Fatal("credential account name is not bound to both Cloud ID and account ID")
	}
}

func TestNativeStoreErrorDoesNotFormatToken(t *testing.T) {
	keyring.MockInitWithError(errors.New("credential store locked"))
	store := credential.NewNativeStore()
	token, _ := secret.NewToken("never-print-this")
	err := store.Set(credential.IdentityKey{CloudID: "cloud", AccountID: "account"}, token)
	if err == nil {
		t.Fatal("Set() unexpectedly succeeded")
	}
	if strings.Contains(err.Error(), token.Value()) {
		t.Fatalf("Set() error exposed token: %v", err)
	}
	if !strings.Contains(err.Error(), "unlock") {
		t.Errorf("Set() error = %q, want actionable unlock guidance", err)
	}
}
