//go:build !clocktest

package main

import "github.com/JirakLu/clock/internal/jira"

func newJiraIdentityClient() *jira.IdentityClient {
	return jira.NewIdentityClient(nil)
}
