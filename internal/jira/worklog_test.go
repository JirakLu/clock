package jira

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/JirakLu/clock/internal/app/recording"
	"github.com/JirakLu/clock/internal/config"
	"github.com/JirakLu/clock/internal/jiraidentity"
	"github.com/JirakLu/clock/internal/secret"
	"github.com/JirakLu/clock/internal/worklog"
)

func TestWorklogClientPagesAndFiltersAuthoredOverlaps(t *testing.T) {
	t.Parallel()

	location := time.FixedZone("CEST", 2*60*60)
	from := time.Date(2026, time.July, 27, 8, 0, 0, 0, location)
	to := time.Date(2026, time.July, 27, 10, 0, 0, 0, location)
	var searchRequests, clockWorklogRequests int
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		email, token, authenticated := request.BasicAuth()
		if !authenticated || email != "person@example.com" || token != "api-token" {
			t.Errorf("BasicAuth() = (%q, %q, %v)", email, token, authenticated)
		}
		switch {
		case request.URL.Path == "/ex/jira/cloud/rest/api/3/search/jql":
			searchRequests++
			if request.Method != http.MethodPost {
				t.Errorf("search method = %s, want POST", request.Method)
			}
			var body struct {
				JQL           string   `json:"jql"`
				Fields        []string `json:"fields"`
				NextPageToken string   `json:"nextPageToken"`
			}
			if err := json.NewDecoder(request.Body).Decode(&body); err != nil {
				t.Fatal(err)
			}
			for _, want := range []string{
				"worklogAuthor = currentUser()",
			} {
				if !strings.Contains(body.JQL, want) {
					t.Errorf("search JQL = %q, want %q", body.JQL, want)
				}
			}
			if len(body.Fields) != 1 || body.Fields[0] != "summary" {
				t.Errorf("search fields = %v", body.Fields)
			}
			if !strings.Contains(body.JQL, "worklogDate") {
				_ = json.NewEncoder(response).Encode(map[string]any{
					"issues": []any{map[string]any{
						"key": "LONG-3", "fields": map[string]any{"summary": "Long Worklog"},
					}},
					"isLast": true,
				})
				return
			}
			for _, want := range []string{
				`worklogDate >= "2026-07-26"`,
				`worklogDate <= "2026-07-28"`,
			} {
				if !strings.Contains(body.JQL, want) {
					t.Errorf("range search JQL = %q, want %q", body.JQL, want)
				}
			}
			if body.NextPageToken == "" {
				_ = json.NewEncoder(response).Encode(map[string]any{
					"issues": []any{map[string]any{
						"key": "CLOCK-13", "fields": map[string]any{"summary": "Create Worklogs"},
					}},
					"nextPageToken": "next-search-page",
					"isLast":        false,
				})
				return
			}
			if body.NextPageToken != "next-search-page" {
				t.Errorf("nextPageToken = %q", body.NextPageToken)
			}
			_ = json.NewEncoder(response).Encode(map[string]any{
				"issues": []any{map[string]any{
					"key": "OTHER-2", "fields": map[string]any{"summary": "Other issue"},
				}},
				"isLast": true,
			})
		case request.URL.Path == "/ex/jira/cloud/rest/api/3/issue/CLOCK-13/worklog":
			clockWorklogRequests++
			if request.URL.Query().Get("startedAfter") == "" ||
				request.URL.Query().Get("startedBefore") == "" {
				t.Errorf("worklog range query = %q, want startedAfter and startedBefore", request.URL.RawQuery)
			}
			if got, want := request.URL.Query().Get("startedAfter"), "0"; got != want {
				t.Errorf("startedAfter = %q, want %q", got, want)
			}
			if got, want := request.URL.Query().Get("startedBefore"), strconv.FormatInt(to.AddDate(0, 0, 1).UnixMilli(), 10); got != want {
				t.Errorf("startedBefore = %q, want %q", got, want)
			}
			if clockWorklogRequests == 1 {
				_ = json.NewEncoder(response).Encode(map[string]any{
					"startAt": 0, "maxResults": 1, "total": 2,
					"worklogs": []any{
						jiraWorklogDocument("100", "account", "2026-07-27T08:30:00.000+0200", 3600, "Focused work"),
					},
				})
				return
			}
			if request.URL.Query().Get("startAt") != "1" {
				t.Errorf("worklog startAt = %q, want 1", request.URL.Query().Get("startAt"))
			}
			_ = json.NewEncoder(response).Encode(map[string]any{
				"startAt": 1, "maxResults": 1, "total": 2,
				"worklogs": []any{
					jiraWorklogDocument("101", "someone-else", "2026-07-27T09:00:00.000+0200", 1800, ""),
				},
			})
		case request.URL.Path == "/ex/jira/cloud/rest/api/3/issue/OTHER-2/worklog":
			_ = json.NewEncoder(response).Encode(map[string]any{
				"startAt": 0, "maxResults": 100, "total": 1,
				"worklogs": []any{
					jiraWorklogDocument("102", "account", "2026-07-27T06:00:00.000+0200", 1800, ""),
				},
			})
		case request.URL.Path == "/ex/jira/cloud/rest/api/3/issue/LONG-3/worklog":
			_ = json.NewEncoder(response).Encode(map[string]any{
				"startAt": 0, "maxResults": 100, "total": 1,
				"worklogs": []any{
					jiraWorklogDocument("long", "account", "2026-07-20T08:00:00.000+0200", 606600, ""),
				},
			})
		default:
			http.NotFound(response, request)
		}
	}))
	defer server.Close()

	client := newIdentityClient(server.Client(), nil, server.URL)
	got, err := client.ListAuthoredWorklogs(
		context.Background(), testAuth(), from, to,
	)
	if err != nil {
		t.Fatalf("ListAuthoredWorklogs() error = %v", err)
	}
	if searchRequests != 3 || clockWorklogRequests != 2 {
		t.Errorf("page request counts = search %d, worklog %d", searchRequests, clockWorklogRequests)
	}
	if len(got) != 2 {
		t.Fatalf("ListAuthoredWorklogs() returned %d Worklogs, want 2: %#v", len(got), got)
	}
	if got[0].ID != "100" || got[0].Issue.String() != "CLOCK-13" ||
		got[0].Summary != "Create Worklogs" || got[0].Description != "Focused work" {
		t.Errorf("Worklog = %#v", got[0])
	}
	if got[1].ID != "long" || got[1].Issue != "LONG-3" ||
		!got[1].Interval.Start().Before(from.AddDate(0, 0, -1)) ||
		!got[1].Interval.End().After(from) {
		t.Errorf("long overlapping Worklog = %#v", got[1])
	}
}

func TestWorklogClientCreatesExactJiraContract(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name        string
		description string
		wantComment bool
	}{
		{name: "with ADF description", description: "Pairing session", wantComment: true},
		{name: "without comment", wantComment: false},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
				if request.Method != http.MethodPost {
					t.Errorf("method = %s, want POST", request.Method)
				}
				if got := request.URL.Path; got != "/ex/jira/cloud/rest/api/3/issue/CLOCK-13/worklog" {
					t.Errorf("path = %q", got)
				}
				if got := request.URL.Query().Get("adjustEstimate"); got != "leave" {
					t.Errorf("adjustEstimate = %q, want leave", got)
				}
				var body map[string]any
				if err := json.NewDecoder(request.Body).Decode(&body); err != nil {
					t.Fatal(err)
				}
				if body["started"] != "2026-07-27T09:15:00.000+0200" {
					t.Errorf("started = %#v", body["started"])
				}
				if body["timeSpentSeconds"] != float64(2700) {
					t.Errorf("timeSpentSeconds = %#v", body["timeSpentSeconds"])
				}
				comment, hasComment := body["comment"]
				if hasComment != test.wantComment {
					t.Errorf("comment present = %v, want %v", hasComment, test.wantComment)
				}
				if test.wantComment && !strings.Contains(mustJSON(t, comment), test.description) {
					t.Errorf("comment = %s, want description", mustJSON(t, comment))
				}
				response.WriteHeader(http.StatusCreated)
				_ = json.NewEncoder(response).Encode(
					jiraWorklogDocument("123", "account", "2026-07-27T09:15:00.000+0200", 2700, test.description),
				)
			}))
			defer server.Close()

			start := time.Date(2026, time.July, 27, 9, 15, 0, 0, time.FixedZone("CEST", 2*60*60))
			interval, _ := worklog.NewInterval(start, start.Add(45*time.Minute))
			client := newIdentityClient(server.Client(), nil, server.URL)
			created, err := client.CreateWorklog(context.Background(), testAuth(), worklog.Draft{
				Issue: worklog.IssueKey("CLOCK-13"), Interval: interval, Description: test.description,
			})
			if err != nil {
				t.Fatalf("CreateWorklog() error = %v", err)
			}
			if created.ID != "123" || created.AuthorID != "account" ||
				created.Issue != "CLOCK-13" ||
				!created.Interval.Start().Equal(start) ||
				created.Interval.Seconds() != 2700 ||
				created.Description != test.description {
				t.Errorf("CreateWorklog() = %#v", created)
			}
		})
	}
}

func TestWorklogClientClassifiesCreationFailures(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name        string
		roundTrip   roundTripFunc
		wantOutcome CreateOutcome
		wantDetail  string
	}{
		{
			name: "definite rejection",
			roundTrip: func(*http.Request) (*http.Response, error) {
				return &http.Response{
					StatusCode: http.StatusForbidden,
					Body:       io.NopCloser(strings.NewReader(`{"errorMessages":["forbidden"]}`)),
					Header:     make(http.Header),
				}, nil
			},
			wantOutcome: CreateDefinitelyRejected,
			wantDetail:  "forbidden",
		},
		{
			name: "uncertain transport outcome",
			roundTrip: func(*http.Request) (*http.Response, error) {
				return nil, errors.New("connection reset")
			},
			wantOutcome: CreateOutcomeUncertain,
		},
		{
			name: "uncertain server outcome",
			roundTrip: func(*http.Request) (*http.Response, error) {
				return &http.Response{
					StatusCode: http.StatusBadGateway,
					Body:       io.NopCloser(strings.NewReader(`{"errorMessages":["upstream"]}`)),
					Header:     make(http.Header),
				}, nil
			},
			wantOutcome: CreateOutcomeUncertain,
		},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			start := time.Date(2026, time.July, 27, 9, 15, 0, 0, time.UTC)
			interval, _ := worklog.NewInterval(start, start.Add(time.Minute))
			client := newIdentityClient(&http.Client{Transport: test.roundTrip}, nil, "https://api.example")
			_, err := client.CreateWorklog(context.Background(), testAuth(), worklog.Draft{
				Issue: "CLOCK-13", Interval: interval,
			})
			var failure *CreateFailure
			if !errors.As(err, &failure) {
				t.Fatalf("CreateWorklog() error = %T %v, want *CreateFailure", err, err)
			}
			if failure.Outcome != test.wantOutcome {
				t.Errorf("failure outcome = %v, want %v", failure.Outcome, test.wantOutcome)
			}
			if test.wantDetail != "" && !strings.Contains(err.Error(), test.wantDetail) {
				t.Errorf("CreateWorklog() error = %q, want Jira detail %q", err, test.wantDetail)
			}
		})
	}
}

func TestWorklogClientTreatsMalformedCreatedResponseAsUncertain(t *testing.T) {
	t.Parallel()

	start := time.Date(2026, time.July, 27, 9, 15, 0, 0, time.UTC)
	interval, _ := worklog.NewInterval(start, start.Add(time.Minute))
	client := newIdentityClient(&http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
		return &http.Response{
			StatusCode: http.StatusCreated,
			Body:       io.NopCloser(strings.NewReader(`{"id":"123"}`)),
			Header:     make(http.Header),
		}, nil
	})}, nil, "https://api.example")

	_, err := client.CreateWorklog(
		context.Background(),
		testAuth(),
		worklog.Draft{Issue: "CLOCK-13", Interval: interval},
	)
	var failure *CreateFailure
	if !errors.As(err, &failure) || failure.Outcome != CreateOutcomeUncertain {
		t.Fatalf("CreateWorklog() error = %T %v, want uncertain CreateFailure", err, err)
	}
}

func jiraWorklogDocument(id, author, started string, seconds int64, description string) map[string]any {
	document := map[string]any{
		"id": id, "author": map[string]any{"accountId": author},
		"started": started, "timeSpentSeconds": seconds,
	}
	if description != "" {
		document["comment"] = map[string]any{
			"type": "doc", "version": 1,
			"content": []any{map[string]any{
				"type":    "paragraph",
				"content": []any{map[string]any{"type": "text", "text": description}},
			}},
		}
	}
	return document
}

func testConfiguration() config.Configuration {
	return config.Configuration{JiraIdentity: jiraidentity.Reference{
		SiteURL: "https://example.atlassian.net", CloudID: "cloud",
		Email: "person@example.com", AccountID: "account",
	}}
}

func testToken() secret.Token {
	token, _ := secret.NewToken("api-token")
	return token
}

func testAuth() recording.Auth {
	return recording.Auth{Identity: testConfiguration().JiraIdentity, Token: testToken()}
}

func mustJSON(t *testing.T, value any) string {
	t.Helper()
	data, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(request *http.Request) (*http.Response, error) {
	return f(request)
}
