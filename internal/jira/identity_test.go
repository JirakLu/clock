package jira

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/JirakLu/clock/internal/config"
	"github.com/JirakLu/clock/internal/jiraidentity"
	"github.com/JirakLu/clock/internal/secret"
)

func TestIdentityClientDiscoversCloudAndValidatesMyself(t *testing.T) {
	t.Parallel()

	const (
		email    = "person@example.com"
		tokenRaw = "super-secret-token"
	)
	var server *httptest.Server
	server = httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		switch request.URL.Path {
		case "/tenant":
			if request.Method != http.MethodGet {
				t.Errorf("tenant method = %s, want GET", request.Method)
			}
			_ = json.NewEncoder(response).Encode(map[string]string{"cloudId": "cloud-123"})
		case "/ex/jira/cloud-123/rest/api/3/myself":
			if request.Method != http.MethodGet {
				t.Errorf("myself method = %s, want GET", request.Method)
			}
			gotEmail, gotToken, ok := request.BasicAuth()
			if !ok || gotEmail != email || gotToken != tokenRaw {
				t.Errorf("BasicAuth() = (%q, %q, %v)", gotEmail, gotToken, ok)
			}
			_ = json.NewEncoder(response).Encode(map[string]any{
				"accountId":   "account-456",
				"displayName": "Example Person",
				"active":      true,
			})
		default:
			http.NotFound(response, request)
		}
	}))
	defer server.Close()

	token, _ := secret.NewToken(tokenRaw)
	client := newIdentityClient(server.Client(), func(string) string {
		return server.URL + "/tenant"
	}, server.URL)

	got, err := client.DiscoverAndValidate(
		context.Background(),
		"https://EXAMPLE.atlassian.net/",
		email,
		token,
	)
	if err != nil {
		t.Fatalf("DiscoverAndValidate() error = %v", err)
	}
	want := jiraidentity.Identity{
		Reference: jiraidentity.Reference{
			SiteURL: "https://example.atlassian.net", CloudID: "cloud-123",
			Email: email, AccountID: "account-456",
		},
		DisplayName: "Example Person",
	}
	if got != want {
		t.Errorf("DiscoverAndValidate() = %#v, want %#v", got, want)
	}
}

func TestIdentityClientValidationFailureDoesNotExposeToken(t *testing.T) {
	t.Parallel()

	const tokenRaw = "do-not-print-this"
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		if request.URL.Path == "/tenant" {
			_ = json.NewEncoder(response).Encode(map[string]string{"cloudId": "cloud-123"})
			return
		}
		response.WriteHeader(http.StatusUnauthorized)
		_, _ = response.Write([]byte(`{"errorMessages":["unauthorized"]}`))
	}))
	defer server.Close()

	token, _ := secret.NewToken(tokenRaw)
	client := newIdentityClient(server.Client(), func(string) string {
		return server.URL + "/tenant"
	}, server.URL)
	_, err := client.DiscoverAndValidate(
		context.Background(), "https://example.atlassian.net", "person@example.com", token,
	)
	if err == nil {
		t.Fatal("DiscoverAndValidate() unexpectedly succeeded")
	}
	if strings.Contains(err.Error(), tokenRaw) {
		t.Fatalf("error exposed API token: %v", err)
	}
	if !strings.Contains(err.Error(), "credentials") {
		t.Errorf("error = %q, want actionable credential diagnosis", err)
	}
}

func TestIdentityClientRejectsConfiguredIdentityMismatch(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		_ = json.NewEncoder(response).Encode(map[string]any{
			"accountId": "different-account", "displayName": "Another Person", "active": true,
		})
	}))
	defer server.Close()

	token, _ := secret.NewToken("secret")
	client := newIdentityClient(server.Client(), nil, server.URL)
	configuration := config.Configuration{
		JiraIdentity: jiraidentity.Reference{
			SiteURL: "https://example.atlassian.net", CloudID: "cloud-123",
			Email: "person@example.com", AccountID: "configured-account",
		},
	}

	err := client.VerifyConfiguredIdentity(context.Background(), configuration, token)
	if err == nil {
		t.Fatal("VerifyConfiguredIdentity() unexpectedly succeeded")
	}
	if !strings.Contains(err.Error(), "configured-account") || !strings.Contains(err.Error(), "different-account") {
		t.Errorf("error = %q, want both configured and authenticated account IDs", err)
	}
}
