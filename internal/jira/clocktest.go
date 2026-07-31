//go:build clocktest

package jira

import "net/http"

// NewClocktestIdentityClient exposes endpoint injection only in specially tagged
// compiled-binary contract tests. Release builds cannot select another gateway.
func NewClocktestIdentityClient(httpClient *http.Client, apiGatewayURL string) *IdentityClient {
	return newIdentityClient(httpClient, nil, apiGatewayURL)
}
