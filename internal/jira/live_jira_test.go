//go:build livejira

package jira

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/JirakLu/clock/internal/app/recording"
	"github.com/JirakLu/clock/internal/secret"
	"github.com/JirakLu/clock/internal/worklog"
)

func TestLiveJiraCreateReadAndCleanup(t *testing.T) {
	siteURL := requiredLiveJiraEnvironment(t, "CLOCK_LIVE_JIRA_SITE_URL")
	email := requiredLiveJiraEnvironment(t, "CLOCK_LIVE_JIRA_EMAIL")
	tokenRaw := requiredLiveJiraEnvironment(t, "CLOCK_LIVE_JIRA_TOKEN")
	issueRaw := requiredLiveJiraEnvironment(t, "CLOCK_LIVE_JIRA_ISSUE")
	token, ok := secret.NewToken(tokenRaw)
	if !ok {
		t.Fatal("CLOCK_LIVE_JIRA_TOKEN is invalid")
	}
	issue, err := worklog.ParseIssueKey(issueRaw)
	if err != nil {
		t.Fatalf("CLOCK_LIVE_JIRA_ISSUE is invalid: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Minute)
	defer cancel()
	client := NewIdentityClient(nil)
	identity, err := client.DiscoverAndValidate(ctx, siteURL, email, token)
	if err != nil {
		t.Fatalf("validate configured Jira identity: %v", err)
	}
	auth := recording.Auth{Identity: identity.Reference, Token: token}
	assertLiveJiraIssueAccessible(t, ctx, auth, issue)

	marker := "clock-live-" + randomHex(t, 8)
	start := time.Now().UTC().Add(-2 * time.Minute).Truncate(time.Second)
	interval, err := worklog.NewInterval(start, start.Add(time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	created, err := client.CreateWorklog(ctx, auth, worklog.Draft{
		Issue: issue, Interval: interval, Description: marker,
	})
	if err != nil {
		t.Fatalf("create uniquely marked Jira Worklog (not retried): %v", err)
	}
	t.Cleanup(func() {
		cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cleanupCancel()
		if err := deleteLiveJiraWorklog(cleanupCtx, auth, issue, created.ID); err != nil {
			t.Errorf("clean up live Jira Worklog %s: %v", created.ID, err)
		}
	})

	got, err := client.ListAuthoredWorklogs(ctx, auth, start.Add(-time.Minute), start.Add(2*time.Minute))
	if err != nil {
		t.Fatalf("read created Worklog through authored reporting path: %v", err)
	}
	for _, item := range got {
		if item.ID == created.ID && item.AuthorID == identity.AccountID && item.Description == marker {
			return
		}
	}
	t.Fatalf("created Worklog %s was not returned with its author and marker", created.ID)
}

func requiredLiveJiraEnvironment(t *testing.T, name string) string {
	t.Helper()
	value := strings.TrimSpace(os.Getenv(name))
	if value == "" {
		t.Fatalf("%s must be set for the opt-in live Jira suite", name)
	}
	return value
}

func randomHex(t *testing.T, bytes int) string {
	t.Helper()
	value := make([]byte, bytes)
	if _, err := rand.Read(value); err != nil {
		t.Fatalf("create unique Worklog marker: %v", err)
	}
	return hex.EncodeToString(value)
}

func assertLiveJiraIssueAccessible(
	t *testing.T,
	ctx context.Context,
	auth recording.Auth,
	issue worklog.IssueKey,
) {
	t.Helper()
	requestURL := gatewayURL + "/ex/jira/" + url.PathEscape(string(auth.Identity.CloudID)) +
		"/rest/api/3/issue/" + url.PathEscape(issue.String()) + "?fields=key"
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, requestURL, nil)
	if err != nil {
		t.Fatal(err)
	}
	setJiraHeaders(request, auth.Identity, auth.Token)
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatalf("query configured Jira issue: %v", err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		t.Fatalf("query configured Jira issue: Jira returned HTTP %d", response.StatusCode)
	}
}

func deleteLiveJiraWorklog(
	ctx context.Context,
	auth recording.Auth,
	issue worklog.IssueKey,
	worklogID string,
) error {
	requestURL := gatewayURL + "/ex/jira/" + url.PathEscape(string(auth.Identity.CloudID)) +
		"/rest/api/3/issue/" + url.PathEscape(issue.String()) + "/worklog/" + url.PathEscape(worklogID) +
		"?adjustEstimate=leave"
	request, err := http.NewRequestWithContext(ctx, http.MethodDelete, requestURL, nil)
	if err != nil {
		return err
	}
	setJiraHeaders(request, auth.Identity, auth.Token)
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusNoContent {
		return fmt.Errorf("Jira returned HTTP %d", response.StatusCode)
	}
	return nil
}
