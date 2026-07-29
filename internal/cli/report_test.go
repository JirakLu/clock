package cli_test

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
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
	} {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			selector := map[string]report.Selector{
				"day": report.Today, "week": report.LastWeek,
				"month": report.LastMonth, "json_day": report.Today,
				"json_week": report.LastWeek, "json_month": report.LastMonth,
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
	}
	window, _ := report.NewWindow(selector, from, to, location)
	return appreport.Result{
		Report:          report.Build(window, source),
		IncludeEarnings: true,
		HourlyRate:      mustRate(t, "750.00"),
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
