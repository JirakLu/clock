package cli_test

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	appreport "github.com/JirakLu/clock/internal/app/report"
	"github.com/JirakLu/clock/internal/cli"
	"github.com/JirakLu/clock/internal/earnings"
	"github.com/JirakLu/clock/internal/report"
	"github.com/JirakLu/clock/internal/worklog"
)

func TestFreshRootReportAcceptsPresetsAndExplicitBounds(t *testing.T) {
	t.Parallel()

	prague, _ := time.LoadLocation("Europe/Prague")
	tests := []struct {
		name         string
		args         []string
		wantSelector report.Selector
		wantFrom     time.Time
		wantTo       time.Time
	}{
		{name: "today", args: []string{"report", "today"}, wantSelector: report.Today},
		{name: "last week", args: []string{"report", "last-week"}, wantSelector: report.LastWeek},
		{name: "last month", args: []string{"report", "last-month"}, wantSelector: report.LastMonth},
		{
			name: "local dates", args: []string{"report", "--from", "2026-07-01", "--to", "2026-08-01"},
			wantSelector: report.Explicit,
			wantFrom:     time.Date(2026, time.July, 1, 0, 0, 0, 0, prague),
			wantTo:       time.Date(2026, time.August, 1, 0, 0, 0, 0, prague),
		},
		{
			name: "offset bounds", args: []string{
				"report", "--from", "2026-07-01T09:15+01:00",
				"--to", "2026-07-01T10:45+01:00", "--earnings", "--json",
			},
			wantSelector: report.Explicit,
			wantFrom:     time.Date(2026, time.July, 1, 9, 15, 0, 0, time.FixedZone("", 3600)),
			wantTo:       time.Date(2026, time.July, 1, 10, 45, 0, 0, time.FixedZone("", 3600)),
		},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			runner := &fakeReportRunner{result: emptyReportResult(t, test.wantSelector, prague)}
			var stdout, stderr bytes.Buffer
			root := cli.NewRoot(cli.RootOptions{
				Report: runner, Out: &stdout, Err: &stderr, Location: prague,
			})
			root.SetArgs(test.args)
			if exitCode := cli.Execute(root); exitCode != 0 {
				t.Fatalf("Execute() exit = %d, stderr = %s", exitCode, stderr.String())
			}
			if runner.input.Selector != test.wantSelector {
				t.Errorf("selector = %v, want %v", runner.input.Selector, test.wantSelector)
			}
			if !test.wantFrom.IsZero() &&
				(!runner.input.From.Equal(test.wantFrom) || !runner.input.To.Equal(test.wantTo)) {
				t.Errorf("bounds = [%v, %v), want [%v, %v)", runner.input.From, runner.input.To, test.wantFrom, test.wantTo)
			}
			if strings.Contains(strings.Join(test.args, " "), "--earnings") != runner.input.Earnings {
				t.Errorf("earnings = %v", runner.input.Earnings)
			}
		})
	}
}

func TestFreshRootReportRejectsInvalidGrammarBeforeApplication(t *testing.T) {
	t.Parallel()

	prague, _ := time.LoadLocation("Europe/Prague")
	tests := []struct {
		name string
		args []string
		want string
	}{
		{name: "missing to", args: []string{"report", "--from", "2026-07-01"}, want: "required together"},
		{name: "missing from", args: []string{"report", "--to", "2026-07-02"}, want: "required together"},
		{name: "preset conflict", args: []string{"report", "today", "--from", "2026-07-01", "--to", "2026-07-02"}, want: "conflict"},
		{name: "non increasing", args: []string{"report", "--from", "2026-07-02", "--to", "2026-07-01"}, want: "after"},
		{name: "ambiguous", args: []string{"report", "--from", "2026-10-25T02:30", "--to", "2026-10-25T04:00"}, want: "ambiguous"},
		{name: "unexpected selector", args: []string{"report", "this-week"}, want: "unknown command"},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			runner := &fakeReportRunner{}
			var stdout, stderr bytes.Buffer
			root := cli.NewRoot(cli.RootOptions{
				Report: runner, Out: &stdout, Err: &stderr, Location: prague,
			})
			root.SetArgs(test.args)
			if exitCode := cli.Execute(root); exitCode == 0 {
				t.Fatal("Execute() unexpectedly succeeded")
			}
			if runner.calls != 0 || stdout.Len() != 0 || !strings.Contains(stderr.String(), test.want) {
				t.Errorf("calls = %d, stdout = %q, stderr = %q, want %q", runner.calls, stdout.String(), stderr.String(), test.want)
			}
		})
	}
}

func TestReportGoldenFixtures(t *testing.T) {
	t.Parallel()

	prague, _ := time.LoadLocation("Europe/Prague")
	for _, test := range []struct {
		name string
		args []string
	}{
		{name: "day", args: []string{"report", "today", "--earnings"}},
		{name: "week", args: []string{"report", "last-week", "--earnings"}},
		{name: "month", args: []string{"report", "last-month", "--earnings"}},
		{name: "json_day", args: []string{"report", "today", "--earnings", "--json"}},
		{name: "json_week", args: []string{"report", "last-week", "--earnings", "--json"}},
		{name: "json_month", args: []string{"report", "last-month", "--earnings", "--json"}},
		{
			name: "json_explicit",
			args: []string{
				"report", "--from", "2026-07-01", "--to", "2026-07-04",
				"--earnings", "--json",
			},
		},
	} {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			selector := map[string]report.Selector{
				"day": report.Today, "week": report.LastWeek,
				"month": report.LastMonth, "json_day": report.Today,
				"json_week": report.LastWeek, "json_month": report.LastMonth,
				"json_explicit": report.Explicit,
			}[test.name]
			runner := &fakeReportRunner{result: goldenReportResult(t, selector, prague)}
			var stdout, stderr bytes.Buffer
			root := cli.NewRoot(cli.RootOptions{
				Report: runner, Out: &stdout, Err: &stderr, Location: prague,
			})
			root.SetArgs(test.args)
			if exitCode := cli.Execute(root); exitCode != 0 {
				t.Fatalf("Execute() exit = %d, stderr = %s", exitCode, stderr.String())
			}
			want, err := os.ReadFile(filepath.Join("testdata", "report_"+test.name+".golden"))
			if err != nil {
				t.Fatal(err)
			}
			if stdout.String() != string(want) {
				t.Errorf("output mismatch\n--- got ---\n%s\n--- want ---\n%s", stdout.String(), want)
			}
		})
	}
}

func TestReportJSONOmitsUnrequestedEarningsAndAbsentDescription(t *testing.T) {
	t.Parallel()

	prague, _ := time.LoadLocation("Europe/Prague")
	result := goldenReportResult(t, report.Today, prague)
	result.IncludeEarnings = false
	result.Report.Contributions[0].Description = ""
	runner := &fakeReportRunner{result: result}
	var stdout, stderr bytes.Buffer
	root := cli.NewRoot(cli.RootOptions{
		Report: runner, Out: &stdout, Err: &stderr, Location: prague,
	})
	root.SetArgs([]string{"report", "today", "--json"})
	if exitCode := cli.Execute(root); exitCode != 0 {
		t.Fatalf("Execute() exit = %d, stderr = %s", exitCode, stderr.String())
	}
	if strings.Contains(stdout.String(), `"description"`) ||
		strings.Contains(stdout.String(), `"earnings_czk"`) {
		t.Errorf("optional fields were not omitted: %s", stdout.String())
	}
}

func TestReportJSONV1DTOContract(t *testing.T) {
	t.Parallel()

	prague, _ := time.LoadLocation("Europe/Prague")
	runner := &fakeReportRunner{result: goldenReportResult(t, report.Explicit, prague)}
	var stdout, stderr bytes.Buffer
	root := cli.NewRoot(cli.RootOptions{
		Report: runner, Out: &stdout, Err: &stderr, Location: prague,
	})
	root.SetArgs([]string{
		"report", "--from", "2026-07-01", "--to", "2026-07-04",
		"--earnings", "--json",
	})
	if exitCode := cli.Execute(root); exitCode != 0 {
		t.Fatalf("Execute() exit = %d, stderr = %s", exitCode, stderr.String())
	}

	decoder := json.NewDecoder(&stdout)
	decoder.DisallowUnknownFields()
	var document reportJSONV1
	if err := decoder.Decode(&document); err != nil {
		t.Fatalf("decode clock.report.v1: %v", err)
	}
	if err := decoder.Decode(&struct{}{}); err != io.EOF {
		t.Fatalf("clock.report.v1 has trailing JSON: %v", err)
	}

	if document.Schema != "clock.report.v1" ||
		document.Selector != (selectorJSONV1{Type: "explicit", Value: "explicit"}) {
		t.Errorf("identity = schema %q, selector %#v", document.Schema, document.Selector)
	}
	if document.Window.Timezone != "Europe/Prague" {
		t.Errorf("timezone = %q", document.Window.Timezone)
	}
	for _, bound := range []string{document.Window.From, document.Window.To} {
		if _, err := time.Parse(time.RFC3339, bound); err != nil {
			t.Errorf("window bound %q is not offset-bearing RFC 3339: %v", bound, err)
		}
	}
	if len(document.Contributions) != 2 ||
		document.Contributions[0].WorklogID != document.Contributions[1].WorklogID {
		t.Errorf("split contributions = %#v", document.Contributions)
	}
	if len(document.DailyTotals) != 3 || document.Total.Seconds != 2 {
		t.Errorf("aggregates = daily %#v, total %#v", document.DailyTotals, document.Total)
	}
	money := regexp.MustCompile(`^[0-9]+\.[0-9]{2}$`)
	for _, aggregate := range append(document.DailyTotals, document.Total) {
		if !money.MatchString(aggregate.EarningsCZK) {
			t.Errorf("earnings_czk = %q, want a two-decimal string", aggregate.EarningsCZK)
		}
	}
}

func TestMonthTerminalOmitsUnrequestedEarnings(t *testing.T) {
	t.Parallel()

	prague, _ := time.LoadLocation("Europe/Prague")
	result := goldenReportResult(t, report.LastMonth, prague)
	result.IncludeEarnings = false
	runner := &fakeReportRunner{result: result}
	var stdout, stderr bytes.Buffer
	root := cli.NewRoot(cli.RootOptions{
		Report: runner, Out: &stdout, Err: &stderr, Location: prague,
	})
	root.SetArgs([]string{"report", "last-month"})
	if exitCode := cli.Execute(root); exitCode != 0 {
		t.Fatalf("Execute() exit = %d, stderr = %s", exitCode, stderr.String())
	}
	if strings.Contains(stdout.String(), "EARNINGS") || strings.Contains(stdout.String(), "CZK") {
		t.Errorf("unrequested Earnings appeared: %s", stdout.String())
	}
}

func emptyReportResult(t *testing.T, selector report.Selector, location *time.Location) appreport.Result {
	t.Helper()
	window, err := report.NewWindow(
		selector,
		time.Date(2026, time.July, 29, 0, 0, 0, 0, location),
		time.Date(2026, time.July, 30, 0, 0, 0, 0, location),
		location,
	)
	if err != nil {
		t.Fatal(err)
	}
	return appreport.Result{Report: report.Build(window, nil)}
}

func goldenReportResult(t *testing.T, selector report.Selector, location *time.Location) appreport.Result {
	t.Helper()
	var from, to time.Time
	var source []worklog.Worklog
	switch selector {
	case report.Today:
		from = time.Date(2026, time.July, 29, 0, 0, 0, 0, location)
		to = from.AddDate(0, 0, 1)
		source = []worklog.Worklog{
			reportWorklog(t, "10002", "CLOCK-16", "Reports", "Build report output",
				from.Add(9*time.Hour), from.Add(10*time.Hour+30*time.Minute)),
			reportWorklog(t, "10001", "OPS-42", "Deploy API", "",
				from.Add(10*time.Hour), from.Add(10*time.Hour+45*time.Minute)),
		}
	case report.LastWeek:
		from = time.Date(2026, time.October, 19, 0, 0, 0, 0, location)
		to = from.AddDate(0, 0, 7)
		source = []worklog.Worklog{
			reportWorklog(t, "10003", "CLOCK-16", "Reports", "Across midnight",
				time.Date(2026, time.October, 22, 23, 30, 0, 0, location),
				time.Date(2026, time.October, 23, 0, 30, 0, 0, location)),
			reportWorklog(t, "10004", "OPS-42", "DST deployment", "",
				time.Date(2026, time.October, 25, 0, 0, 0, 0, location),
				time.Date(2026, time.October, 26, 0, 0, 0, 0, location)),
		}
	case report.LastMonth:
		from = time.Date(2026, time.June, 1, 0, 0, 0, 0, location)
		to = time.Date(2026, time.July, 1, 0, 0, 0, 0, location)
		source = []worklog.Worklog{
			reportWorklog(t, "10005", "CLOCK-16", "Reports", "Monthly summary",
				from.AddDate(0, 0, 1).Add(9*time.Hour),
				from.AddDate(0, 0, 1).Add(10*time.Hour+30*time.Minute)),
		}
	case report.Explicit:
		from = time.Date(2026, time.July, 1, 0, 0, 0, 0, location)
		to = time.Date(2026, time.July, 4, 0, 0, 0, 0, location)
		source = []worklog.Worklog{
			reportWorklog(
				t, "10006", "CLOCK-17", "Stable JSON", "Cross local midnight",
				time.Date(2026, time.July, 1, 23, 59, 59, 0, location),
				time.Date(2026, time.July, 2, 0, 0, 1, 0, location),
			),
		}
	}
	window, _ := report.NewWindow(selector, from, to, location)
	rate := "750.00"
	if selector == report.Explicit {
		rate = "18.00"
	}
	return appreport.Result{
		Report:          report.Build(window, source),
		IncludeEarnings: true,
		HourlyRate:      mustRate(t, rate),
	}
}

func reportWorklog(
	t *testing.T,
	id string,
	issue worklog.IssueKey,
	summary string,
	description string,
	from time.Time,
	to time.Time,
) worklog.Worklog {
	t.Helper()
	interval, err := worklog.NewInterval(from, to)
	if err != nil {
		t.Fatal(err)
	}
	return worklog.Worklog{
		ID: id, Issue: issue, Summary: summary, Description: description, Interval: interval,
	}
}

func mustRate(t *testing.T, raw string) earnings.HourlyRate {
	t.Helper()
	rate, err := earnings.ParseHourlyRate(raw)
	if err != nil {
		t.Fatal(err)
	}
	return rate
}

type fakeReportRunner struct {
	input  appreport.Input
	result appreport.Result
	err    error
	calls  int
}

func (f *fakeReportRunner) Run(_ context.Context, input appreport.Input) (appreport.Result, error) {
	f.calls++
	f.input = input
	return f.result, f.err
}

type reportJSONV1 struct {
	Schema        string               `json:"schema"`
	Selector      selectorJSONV1       `json:"selector"`
	Window        windowJSONV1         `json:"window"`
	Contributions []contributionJSONV1 `json:"contributions"`
	DailyTotals   []aggregateJSONV1    `json:"daily_totals"`
	Total         aggregateJSONV1      `json:"total"`
}

type selectorJSONV1 struct {
	Type  string `json:"type"`
	Value string `json:"value"`
}

type windowJSONV1 struct {
	From     string `json:"from"`
	To       string `json:"to"`
	Timezone string `json:"timezone"`
}

type contributionJSONV1 struct {
	WorklogID   string      `json:"worklog_id"`
	Issue       issueJSONV1 `json:"issue"`
	Description string      `json:"description,omitempty"`
	From        string      `json:"from"`
	To          string      `json:"to"`
	Seconds     int64       `json:"seconds"`
}

type issueJSONV1 struct {
	Key     string `json:"key"`
	Summary string `json:"summary"`
}

type aggregateJSONV1 struct {
	Date        string `json:"date,omitempty"`
	Seconds     int64  `json:"seconds"`
	EarningsCZK string `json:"earnings_czk,omitempty"`
}
