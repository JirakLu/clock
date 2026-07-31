package report_test

import (
	"testing"
	"time"

	"github.com/JirakLu/clock/internal/report"
	"github.com/JirakLu/clock/internal/worklog"
)

func TestPresetsResolveMachineLocalCalendarWindows(t *testing.T) {
	t.Parallel()

	prague := mustLocation(t, "Europe/Prague")
	now := time.Date(2026, time.July, 29, 14, 30, 0, 0, prague)
	tests := []struct {
		selector report.Selector
		from     time.Time
		to       time.Time
	}{
		{
			selector: report.Today,
			from:     time.Date(2026, time.July, 29, 0, 0, 0, 0, prague),
			to:       time.Date(2026, time.July, 30, 0, 0, 0, 0, prague),
		},
		{
			selector: report.LastWeek,
			from:     time.Date(2026, time.July, 20, 0, 0, 0, 0, prague),
			to:       time.Date(2026, time.July, 27, 0, 0, 0, 0, prague),
		},
		{
			selector: report.LastMonth,
			from:     time.Date(2026, time.June, 1, 0, 0, 0, 0, prague),
			to:       time.Date(2026, time.July, 1, 0, 0, 0, 0, prague),
		},
	}
	for _, test := range tests {
		test := test
		t.Run(test.selector.Value(), func(t *testing.T) {
			t.Parallel()
			window, err := report.ResolvePreset(test.selector, now, prague)
			if err != nil {
				t.Fatalf("ResolvePreset() error = %v", err)
			}
			if !window.From.Equal(test.from) || !window.To.Equal(test.to) {
				t.Errorf("window = [%v, %v), want [%v, %v)", window.From, window.To, test.from, test.to)
			}
			if window.Timezone != "Europe/Prague" {
				t.Errorf("timezone = %q", window.Timezone)
			}
		})
	}
}

func TestBuildClipsSplitsAndTotalsAcrossDaylightSaving(t *testing.T) {
	t.Parallel()

	prague := mustLocation(t, "Europe/Prague")
	from := time.Date(2026, time.October, 24, 23, 30, 0, 0, prague)
	to := time.Date(2026, time.October, 26, 0, 30, 0, 0, prague)
	window, err := report.NewWindow(report.Explicit, from, to, prague)
	if err != nil {
		t.Fatal(err)
	}
	worklogs := []worklog.Worklog{
		newWorklog(t, "20", "CLOCK-20", "DST work", "Crossing midnight",
			from.Add(-time.Hour), to.Add(time.Hour)),
		newWorklog(t, "10", "CLOCK-10", "Overlap", "",
			time.Date(2026, time.October, 25, 1, 0, 0, 0, prague),
			time.Date(2026, time.October, 25, 3, 0, 0, 0, prague)),
		newWorklog(t, "outside", "CLOCK-30", "Outside", "", to, to.Add(time.Hour)),
	}

	got := report.Build(window, worklogs)
	if len(got.Contributions) != 4 {
		t.Fatalf("contributions = %#v, want 4", got.Contributions)
	}
	wantIDs := []string{"20", "20", "10", "20"}
	wantSeconds := []int64{1800, 90000, 10800, 1800}
	for index, contribution := range got.Contributions {
		if contribution.WorklogID != wantIDs[index] || contribution.Seconds != wantSeconds[index] {
			t.Errorf("contribution %d = %#v, want ID %q and %d seconds", index, contribution, wantIDs[index], wantSeconds[index])
		}
	}
	if got.Contributions[0].Description != "Crossing midnight" ||
		got.Contributions[0].Issue.String() != "CLOCK-20" ||
		got.Contributions[0].Summary != "DST work" {
		t.Errorf("identity was not retained: %#v", got.Contributions[0])
	}
	if len(got.DailyTotals) != 3 ||
		got.DailyTotals[0].Seconds != 1800 ||
		got.DailyTotals[1].Seconds != 100800 ||
		got.DailyTotals[2].Seconds != 1800 {
		t.Errorf("daily totals = %#v", got.DailyTotals)
	}
	if got.TotalSeconds != 104400 {
		t.Errorf("total = %d, want 104400", got.TotalSeconds)
	}
}

func TestBuildIncludesZeroDaysAndOrdersTiesByWorklogID(t *testing.T) {
	t.Parallel()

	location := time.UTC
	from := time.Date(2026, time.July, 1, 0, 0, 0, 0, location)
	to := from.AddDate(0, 0, 3)
	window, _ := report.NewWindow(report.Explicit, from, to, location)
	start := from.Add(9 * time.Hour)
	got := report.Build(window, []worklog.Worklog{
		newWorklog(t, "200", "CLOCK-2", "Second", "", start, start.Add(time.Hour)),
		newWorklog(t, "100", "CLOCK-1", "First", "", start, start.Add(time.Hour)),
	})

	if len(got.DailyTotals) != 3 ||
		got.DailyTotals[1].Seconds != 0 ||
		got.DailyTotals[2].Seconds != 0 {
		t.Errorf("daily totals = %#v, want two zero days", got.DailyTotals)
	}
	if got.Contributions[0].WorklogID != "100" || got.Contributions[1].WorklogID != "200" {
		t.Errorf("contribution order = %#v", got.Contributions)
	}
}

func newWorklog(
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

func mustLocation(t *testing.T, name string) *time.Location {
	t.Helper()
	location, err := time.LoadLocation(name)
	if err != nil {
		t.Fatal(err)
	}
	return location
}
