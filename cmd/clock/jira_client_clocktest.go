//go:build clocktest

package main

import (
	"errors"
	"net/http"
	"os"

	"github.com/JirakLu/clock/internal/credential"
	"github.com/JirakLu/clock/internal/jira"
	"github.com/JirakLu/clock/internal/secret"
	"github.com/zalando/go-keyring"
)

func newJiraIdentityClient() *jira.IdentityClient {
	return jira.NewClocktestIdentityClient(http.DefaultClient, os.Getenv("CLOCK_TEST_GATEWAY_URL"))
}

func init() {
	keyring.MockInit()
	token, _ := secret.NewToken("clocktest-token")
	if err := credential.NewNativeStore().Set(credential.IdentityKey{CloudID: "cloud", AccountID: "account"}, token); err != nil {
		panic(errors.New("initialize clocktest credential: " + err.Error()))
	}
}
