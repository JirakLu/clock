package jira

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/JirakLu/clock/internal/config"
	"github.com/JirakLu/clock/internal/jiraidentity"
	"github.com/JirakLu/clock/internal/secret"
)

const gatewayURL = "https://api.atlassian.com"

type IdentityClient struct {
	httpClient    *http.Client
	tenantInfoURL func(string) string
	gatewayURL    string
}

func NewIdentityClient(httpClient *http.Client) *IdentityClient {
	if httpClient == nil {
		httpClient = &http.Client{Timeout: 30 * time.Second}
	}
	return newIdentityClient(httpClient, func(siteURL string) string {
		return siteURL + "/_edge/tenant_info"
	}, gatewayURL)
}

func newIdentityClient(
	httpClient *http.Client,
	tenantInfoURL func(string) string,
	apiGatewayURL string,
) *IdentityClient {
	return &IdentityClient{
		httpClient: httpClient, tenantInfoURL: tenantInfoURL,
		gatewayURL: strings.TrimRight(apiGatewayURL, "/"),
	}
}

func (c *IdentityClient) DiscoverAndValidate(
	ctx context.Context,
	rawSiteURL string,
	email string,
	token secret.Token,
) (jiraidentity.Identity, error) {
	siteURL, err := config.CanonicalSiteURL(rawSiteURL)
	if err != nil {
		return jiraidentity.Identity{}, err
	}
	if strings.TrimSpace(email) == "" {
		return jiraidentity.Identity{}, errors.New("Atlassian email must not be empty")
	}
	if token.Empty() {
		return jiraidentity.Identity{}, errors.New("Jira API token must not be empty")
	}

	cloudID, err := c.discoverCloudID(ctx, siteURL)
	if err != nil {
		return jiraidentity.Identity{}, err
	}
	account, err := c.myself(ctx, cloudID, email, token)
	if err != nil {
		return jiraidentity.Identity{}, err
	}
	return jiraidentity.Identity{
		Reference: jiraidentity.Reference{
			SiteURL: siteURL, CloudID: cloudID, Email: email,
			AccountID: account.AccountID,
		},
		DisplayName: account.DisplayName,
	}, nil
}

func (c *IdentityClient) VerifyConfiguredIdentity(
	ctx context.Context,
	configuration config.Configuration,
	token secret.Token,
) error {
	return c.verifyIdentity(ctx, configuration.JiraIdentity, token)
}

func (c *IdentityClient) verifyIdentity(
	ctx context.Context,
	identity jiraidentity.Reference,
	token secret.Token,
) error {
	account, err := c.myself(ctx, identity.CloudID, identity.Email, token)
	if err != nil {
		return err
	}
	if account.AccountID != identity.AccountID {
		return fmt.Errorf(
			"Jira identity mismatch: configuration expects account %q but credentials authenticate account %q",
			identity.AccountID,
			account.AccountID,
		)
	}
	return nil
}

func (c *IdentityClient) discoverCloudID(
	ctx context.Context,
	siteURL string,
) (jiraidentity.CloudID, error) {
	if c.tenantInfoURL == nil {
		return "", errors.New("Jira Cloud ID discovery is unavailable")
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, c.tenantInfoURL(siteURL), nil)
	if err != nil {
		return "", fmt.Errorf("prepare Jira Cloud ID discovery: %w", err)
	}
	response, err := c.httpClient.Do(request)
	if err != nil {
		return "", fmt.Errorf("discover Jira Cloud ID for %q: %w", siteURL, err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return "", fmt.Errorf(
			"discover Jira Cloud ID for %q: site returned HTTP %d; verify the Jira Cloud site URL",
			siteURL,
			response.StatusCode,
		)
	}

	var document struct {
		CloudID string `json:"cloudId"`
	}
	if err := decodeJSON(response.Body, &document); err != nil {
		return "", fmt.Errorf("discover Jira Cloud ID for %q: invalid response: %w", siteURL, err)
	}
	if strings.TrimSpace(document.CloudID) == "" {
		return "", fmt.Errorf("discover Jira Cloud ID for %q: response omitted cloudId", siteURL)
	}
	return jiraidentity.CloudID(document.CloudID), nil
}

type jiraAccount struct {
	AccountID   jiraidentity.AccountID `json:"accountId"`
	DisplayName string                 `json:"displayName"`
	Active      bool                   `json:"active"`
}

func (c *IdentityClient) myself(
	ctx context.Context,
	cloudID jiraidentity.CloudID,
	email string,
	token secret.Token,
) (jiraAccount, error) {
	if strings.TrimSpace(string(cloudID)) == "" {
		return jiraAccount{}, errors.New("Jira Cloud ID must not be empty")
	}
	myselfURL := c.gatewayURL + "/ex/jira/" + url.PathEscape(string(cloudID)) + "/rest/api/3/myself"
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, myselfURL, nil)
	if err != nil {
		return jiraAccount{}, fmt.Errorf("prepare Jira identity validation: %w", err)
	}
	request.SetBasicAuth(email, token.Value())
	request.Header.Set("Accept", "application/json")

	response, err := c.httpClient.Do(request)
	if err != nil {
		return jiraAccount{}, fmt.Errorf("validate Jira identity: %w", err)
	}
	defer response.Body.Close()
	if response.StatusCode == http.StatusUnauthorized || response.StatusCode == http.StatusForbidden {
		return jiraAccount{}, fmt.Errorf(
			"validate Jira identity: Jira rejected the credentials (HTTP %d); verify the email, API token, scopes, and project access",
			response.StatusCode,
		)
	}
	if response.StatusCode != http.StatusOK {
		return jiraAccount{}, fmt.Errorf(
			"validate Jira identity: Jira returned HTTP %d; verify the Cloud ID and Jira availability",
			response.StatusCode,
		)
	}

	var account jiraAccount
	if err := decodeJSON(response.Body, &account); err != nil {
		return jiraAccount{}, fmt.Errorf("validate Jira identity: invalid /myself response: %w", err)
	}
	if strings.TrimSpace(string(account.AccountID)) == "" || strings.TrimSpace(account.DisplayName) == "" {
		return jiraAccount{}, errors.New("validate Jira identity: /myself response omitted accountId or displayName")
	}
	if !account.Active {
		return jiraAccount{}, errors.New("validate Jira identity: the authenticated Jira account is inactive")
	}
	return account, nil
}

func decodeJSON(reader io.Reader, destination any) error {
	decoder := json.NewDecoder(io.LimitReader(reader, 1<<20))
	if err := decoder.Decode(destination); err != nil {
		return err
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		if err == nil {
			return errors.New("response contained multiple JSON values")
		}
		return err
	}
	return nil
}
