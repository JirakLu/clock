package jira

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/JirakLu/clock/internal/app/recording"
	"github.com/JirakLu/clock/internal/jiraidentity"
	"github.com/JirakLu/clock/internal/secret"
	"github.com/JirakLu/clock/internal/worklog"
)

const jiraTimestampLayout = "2006-01-02T15:04:05.000-0700"

type CreateOutcome uint8

const (
	CreateDefinitelyRejected CreateOutcome = iota + 1
	CreateOutcomeUncertain
)

type CreateFailure struct {
	Outcome CreateOutcome
	Cause   error
}

func (e *CreateFailure) Error() string {
	if e == nil {
		return ""
	}
	return e.Cause.Error()
}

func (e *CreateFailure) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.Cause
}

func (e *CreateFailure) Uncertain() bool {
	return e != nil && e.Outcome == CreateOutcomeUncertain
}

func (c *IdentityClient) ListAuthoredWorklogs(
	ctx context.Context,
	auth recording.Auth,
	from time.Time,
	to time.Time,
) ([]worklog.Worklog, error) {
	if !to.After(from) {
		return nil, errors.New("Worklog query end must be after its start")
	}
	issues, err := c.searchWorklogIssues(ctx, auth.Identity, auth.Token, from, to)
	if err != nil {
		return nil, err
	}
	queryInterval, _ := worklog.NewInterval(from, to)
	var result []worklog.Worklog
	for _, issue := range issues {
		issueWorklogs, err := c.issueWorklogs(ctx, auth.Identity, auth.Token, issue, to)
		if err != nil {
			return nil, err
		}
		for _, candidate := range issueWorklogs {
			if candidate.AuthorID == auth.Identity.AccountID &&
				candidate.Interval.Overlaps(queryInterval) {
				result = append(result, candidate)
			}
		}
	}
	return result, nil
}

type candidateIssue struct {
	Key     worklog.IssueKey
	Summary string
}

type jiraWorklogPayload struct {
	ID     string `json:"id"`
	Author struct {
		AccountID string `json:"accountId"`
	} `json:"author"`
	Started          string          `json:"started"`
	TimeSpentSeconds int64           `json:"timeSpentSeconds"`
	Comment          json.RawMessage `json:"comment"`
}

func (c *IdentityClient) searchWorklogIssues(
	ctx context.Context,
	identity jiraidentity.Reference,
	token secret.Token,
	from time.Time,
	to time.Time,
) ([]candidateIssue, error) {
	rangeJQL := fmt.Sprintf(
		`worklogAuthor = currentUser() AND worklogDate >= %q AND worklogDate <= %q ORDER BY key`,
		from.AddDate(0, 0, -1).Format("2006-01-02"),
		to.AddDate(0, 0, 1).Format("2006-01-02"),
	)
	rangeIssues, err := c.searchIssuesByJQL(ctx, identity, token, rangeJQL)
	if err != nil {
		return nil, err
	}
	// The accepted Jira contract requires the widened range query above. Overlap
	// prevention additionally cannot assume a maximum Worklog Duration: an older
	// Worklog may extend into this range. Search every authored issue as a safety
	// supplement, then read its Worklogs from epoch and filter exact overlap.
	allAuthoredIssues, err := c.searchIssuesByJQL(
		ctx,
		identity,
		token,
		"worklogAuthor = currentUser() ORDER BY key",
	)
	if err != nil {
		return nil, err
	}
	seen := make(map[worklog.IssueKey]bool, len(rangeIssues)+len(allAuthoredIssues))
	issues := make([]candidateIssue, 0, len(rangeIssues)+len(allAuthoredIssues))
	for _, issue := range append(rangeIssues, allAuthoredIssues...) {
		if !seen[issue.Key] {
			seen[issue.Key] = true
			issues = append(issues, issue)
		}
	}
	return issues, nil
}

func (c *IdentityClient) searchIssuesByJQL(
	ctx context.Context,
	identity jiraidentity.Reference,
	token secret.Token,
	jql string,
) ([]candidateIssue, error) {
	searchURL := c.gatewayURL + "/ex/jira/" +
		url.PathEscape(string(identity.CloudID)) +
		"/rest/api/3/search/jql"
	nextPageToken := ""
	seen := make(map[worklog.IssueKey]bool)
	var issues []candidateIssue
	for {
		body := struct {
			JQL           string   `json:"jql"`
			Fields        []string `json:"fields"`
			MaxResults    int      `json:"maxResults"`
			NextPageToken string   `json:"nextPageToken,omitempty"`
		}{
			JQL: jql, Fields: []string{"summary"}, MaxResults: 100,
			NextPageToken: nextPageToken,
		}
		data, err := json.Marshal(body)
		if err != nil {
			return nil, fmt.Errorf("encode Jira Worklog issue search: %w", err)
		}
		request, err := http.NewRequestWithContext(ctx, http.MethodPost, searchURL, bytes.NewReader(data))
		if err != nil {
			return nil, fmt.Errorf("prepare Jira Worklog issue search: %w", err)
		}
		setJiraHeaders(request, identity, token)
		response, err := c.httpClient.Do(request)
		if err != nil {
			return nil, fmt.Errorf("search Jira issues containing authored Worklogs: %w", err)
		}
		var document struct {
			Issues []struct {
				Key    string `json:"key"`
				Fields struct {
					Summary string `json:"summary"`
				} `json:"fields"`
			} `json:"issues"`
			NextPageToken string `json:"nextPageToken"`
			IsLast        bool   `json:"isLast"`
		}
		decodeErr := decodeJiraResponse(response, http.StatusOK, &document)
		if decodeErr != nil {
			return nil, fmt.Errorf("search Jira issues containing authored Worklogs: %w", decodeErr)
		}
		for _, item := range document.Issues {
			key, err := worklog.ParseIssueKey(item.Key)
			if err != nil {
				return nil, fmt.Errorf("search Jira issues containing authored Worklogs: invalid issue key: %w", err)
			}
			if !seen[key] {
				seen[key] = true
				issues = append(issues, candidateIssue{Key: key, Summary: item.Fields.Summary})
			}
		}
		if document.IsLast || document.NextPageToken == "" {
			return issues, nil
		}
		if document.NextPageToken == nextPageToken {
			return nil, errors.New("search Jira issues containing authored Worklogs: pagination token did not advance")
		}
		nextPageToken = document.NextPageToken
	}
}

func (c *IdentityClient) issueWorklogs(
	ctx context.Context,
	identity jiraidentity.Reference,
	token secret.Token,
	issue candidateIssue,
	to time.Time,
) ([]worklog.Worklog, error) {
	worklogURL := c.gatewayURL + "/ex/jira/" +
		url.PathEscape(string(identity.CloudID)) +
		"/rest/api/3/issue/" + url.PathEscape(issue.Key.String()) + "/worklog"
	startAt := 0
	var result []worklog.Worklog
	for {
		query := url.Values{}
		query.Set("startedAfter", "0")
		query.Set("startedBefore", strconv.FormatInt(to.AddDate(0, 0, 1).UnixMilli(), 10))
		query.Set("startAt", strconv.Itoa(startAt))
		query.Set("maxResults", "100")
		requestURL := worklogURL + "?" + query.Encode()
		request, err := http.NewRequestWithContext(ctx, http.MethodGet, requestURL, nil)
		if err != nil {
			return nil, fmt.Errorf("prepare Jira Worklog page for %s: %w", issue.Key, err)
		}
		setJiraHeaders(request, identity, token)
		response, err := c.httpClient.Do(request)
		if err != nil {
			return nil, fmt.Errorf("read Jira Worklogs for %s: %w", issue.Key, err)
		}
		var document struct {
			StartAt    int                  `json:"startAt"`
			MaxResults int                  `json:"maxResults"`
			Total      int                  `json:"total"`
			Worklogs   []jiraWorklogPayload `json:"worklogs"`
		}
		if err := decodeJiraResponse(response, http.StatusOK, &document); err != nil {
			return nil, fmt.Errorf("read Jira Worklogs for %s: %w", issue.Key, err)
		}
		for _, item := range document.Worklogs {
			mapped, err := mapJiraWorklog(item, issue)
			if err != nil {
				return nil, err
			}
			result = append(result, mapped)
		}
		next := document.StartAt + len(document.Worklogs)
		if next >= document.Total {
			return result, nil
		}
		if len(document.Worklogs) == 0 || next <= startAt {
			return nil, fmt.Errorf("read Jira Worklogs for %s: pagination did not advance", issue.Key)
		}
		startAt = next
	}
}

func (c *IdentityClient) CreateWorklog(
	ctx context.Context,
	auth recording.Auth,
	draft worklog.Draft,
) (worklog.Worklog, error) {
	createURL := c.gatewayURL + "/ex/jira/" +
		url.PathEscape(string(auth.Identity.CloudID)) +
		"/rest/api/3/issue/" + url.PathEscape(draft.Issue.String()) +
		"/worklog?adjustEstimate=leave"
	body := map[string]any{
		"started":          draft.Interval.Start().Format(jiraTimestampLayout),
		"timeSpentSeconds": draft.Interval.Seconds(),
	}
	if draft.Description != "" {
		body["comment"] = adfComment(draft.Description)
	}
	data, err := json.Marshal(body)
	if err != nil {
		return worklog.Worklog{}, fmt.Errorf("encode Jira Worklog: %w", err)
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, createURL, bytes.NewReader(data))
	if err != nil {
		return worklog.Worklog{}, fmt.Errorf("prepare Jira Worklog creation: %w", err)
	}
	setJiraHeaders(request, auth.Identity, auth.Token)
	response, err := c.httpClient.Do(request)
	if err != nil {
		return worklog.Worklog{}, &CreateFailure{
			Outcome: CreateOutcomeUncertain,
			Cause:   fmt.Errorf("Jira Worklog creation outcome is uncertain: %w", err),
		}
	}
	defer response.Body.Close()
	if response.StatusCode == http.StatusCreated {
		var document jiraWorklogPayload
		if err := decodeJSON(response.Body, &document); err != nil {
			return worklog.Worklog{}, &CreateFailure{
				Outcome: CreateOutcomeUncertain,
				Cause:   fmt.Errorf("Jira created a Worklog but returned an invalid response: %w", err),
			}
		}
		created, err := mapJiraWorklog(document, candidateIssue{Key: draft.Issue})
		if err != nil {
			return worklog.Worklog{}, &CreateFailure{
				Outcome: CreateOutcomeUncertain,
				Cause:   fmt.Errorf("Jira created a Worklog but returned an invalid response: %w", err),
			}
		}
		return created, nil
	}
	detail := jiraErrorDetail(response.Body)
	outcome := CreateDefinitelyRejected
	if response.StatusCode < 400 || response.StatusCode >= 500 {
		outcome = CreateOutcomeUncertain
	}
	message := fmt.Sprintf("Jira returned HTTP %d while creating the Worklog", response.StatusCode)
	if detail != "" {
		message += ": " + detail
	}
	return worklog.Worklog{}, &CreateFailure{
		Outcome: outcome,
		Cause:   errors.New(message),
	}
}

func (c *IdentityClient) VerifyIdentity(ctx context.Context, auth recording.Auth) error {
	return c.verifyIdentity(ctx, auth.Identity, auth.Token)
}

func setJiraHeaders(
	request *http.Request,
	identity jiraidentity.Reference,
	token secret.Token,
) {
	request.SetBasicAuth(identity.Email, token.Value())
	request.Header.Set("Accept", "application/json")
	if request.Body != nil {
		request.Header.Set("Content-Type", "application/json")
	}
}

func decodeJiraResponse(response *http.Response, wantStatus int, destination any) error {
	defer response.Body.Close()
	if response.StatusCode != wantStatus {
		_, _ = io.Copy(io.Discard, io.LimitReader(response.Body, 1<<20))
		return fmt.Errorf("Jira returned HTTP %d", response.StatusCode)
	}
	if err := decodeJSON(response.Body, destination); err != nil {
		return fmt.Errorf("invalid Jira response: %w", err)
	}
	return nil
}

func parseJiraTimestamp(raw string) (time.Time, error) {
	for _, layout := range []string{jiraTimestampLayout, time.RFC3339} {
		parsed, err := time.Parse(layout, raw)
		if err == nil {
			return parsed, nil
		}
	}
	return time.Time{}, fmt.Errorf("invalid started timestamp %q", raw)
}

func mapJiraWorklog(
	item jiraWorklogPayload,
	issue candidateIssue,
) (worklog.Worklog, error) {
	if strings.TrimSpace(item.ID) == "" {
		return worklog.Worklog{}, fmt.Errorf("read Jira Worklog for %s: response omitted id", issue.Key)
	}
	if strings.TrimSpace(item.Author.AccountID) == "" {
		return worklog.Worklog{}, fmt.Errorf("read Jira Worklog %q for %s: response omitted author accountId", item.ID, issue.Key)
	}
	started, err := parseJiraTimestamp(item.Started)
	if err != nil {
		return worklog.Worklog{}, fmt.Errorf("read Jira Worklog %q for %s: %w", item.ID, issue.Key, err)
	}
	if item.TimeSpentSeconds <= 0 {
		return worklog.Worklog{}, fmt.Errorf(
			"read Jira Worklog %q for %s: timeSpentSeconds must be positive",
			item.ID,
			issue.Key,
		)
	}
	interval, _ := worklog.NewInterval(
		started,
		started.Add(time.Duration(item.TimeSpentSeconds)*time.Second),
	)
	return worklog.Worklog{
		ID: item.ID, Issue: issue.Key, Summary: issue.Summary,
		AuthorID: jiraidentity.AccountID(item.Author.AccountID), Interval: interval,
		Description: adfPlainText(item.Comment),
	}, nil
}

func jiraErrorDetail(reader io.Reader) string {
	data, err := io.ReadAll(io.LimitReader(reader, 1<<20))
	if err != nil {
		return ""
	}
	var document struct {
		ErrorMessages []string          `json:"errorMessages"`
		Errors        map[string]string `json:"errors"`
	}
	if json.Unmarshal(data, &document) != nil {
		return ""
	}
	messages := append([]string(nil), document.ErrorMessages...)
	keys := make([]string, 0, len(document.Errors))
	for key := range document.Errors {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		messages = append(messages, key+": "+document.Errors[key])
	}
	return strings.Join(messages, "; ")
}

func adfComment(description string) map[string]any {
	return map[string]any{
		"type": "doc", "version": 1,
		"content": []any{map[string]any{
			"type":    "paragraph",
			"content": []any{map[string]any{"type": "text", "text": description}},
		}},
	}
}

func adfPlainText(raw json.RawMessage) string {
	if len(raw) == 0 || string(raw) == "null" {
		return ""
	}
	var value any
	if json.Unmarshal(raw, &value) != nil {
		return ""
	}
	var text []string
	var visit func(any)
	visit = func(node any) {
		switch current := node.(type) {
		case map[string]any:
			if item, ok := current["text"].(string); ok {
				text = append(text, item)
			}
			for _, child := range current {
				visit(child)
			}
		case []any:
			for _, child := range current {
				visit(child)
			}
		}
	}
	visit(value)
	return strings.Join(text, "")
}
