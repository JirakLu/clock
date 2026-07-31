package worklog_test

import (
	"testing"
	"time"

	"github.com/JirakLu/clock/internal/worklog"
)

func TestParseIssueKeyNormalizesAcceptedKeys(t *testing.T) {
	t.Parallel()

	for raw, want := range map[string]string{
		"clock-13":   "CLOCK-13",
		"OPS_TEAM-7": "OPS_TEAM-7",
	} {
		got, err := worklog.ParseIssueKey(raw)
		if err != nil {
			t.Errorf("ParseIssueKey(%q) error = %v", raw, err)
			continue
		}
		if got.String() != want {
			t.Errorf("ParseIssueKey(%q) = %q, want %q", raw, got, want)
		}
	}
}

func TestParseIssueKeyRejectsNonKeys(t *testing.T) {
	t.Parallel()

	for _, raw := range []string{
		"", "13", "CLOCK", "CLOCK 13", "https://example.atlassian.net/browse/CLOCK-13",
	} {
		if _, err := worklog.ParseIssueKey(raw); err == nil {
			t.Errorf("ParseIssueKey(%q) unexpectedly succeeded", raw)
		}
	}
}

func TestParseCompactDurationAcceptsPositiveHoursAndMinutes(t *testing.T) {
	t.Parallel()

	for raw, want := range map[string]time.Duration{
		"30m":    30 * time.Minute,
		"2h":     2 * time.Hour,
		"2h30m":  2*time.Hour + 30*time.Minute,
		"100h1m": 100*time.Hour + time.Minute,
	} {
		got, err := worklog.ParseCompactDuration(raw)
		if err != nil {
			t.Errorf("ParseCompactDuration(%q) error = %v", raw, err)
			continue
		}
		if got.Duration() != want || got.Seconds() != int64(want/time.Second) {
			t.Errorf("ParseCompactDuration(%q) = %v (%d seconds), want %v", raw, got.Duration(), got.Seconds(), want)
		}
	}
}

func TestParseCompactDurationRejectsUnsupportedForms(t *testing.T) {
	t.Parallel()

	for _, raw := range []string{
		"", "0m", "0h", "0h0m", "-1h", "1.5h", "30s", "1d", "1w",
		"2 h", "2h 30m", "2H", "30M",
	} {
		if _, err := worklog.ParseCompactDuration(raw); err == nil {
			t.Errorf("ParseCompactDuration(%q) unexpectedly succeeded", raw)
		}
	}
}

func TestIntervalOverlapUsesHalfOpenEndpoints(t *testing.T) {
	t.Parallel()

	base := time.Date(2026, time.July, 27, 9, 0, 0, 0, time.UTC)
	target, err := worklog.NewInterval(base, base.Add(time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		name       string
		start, end time.Time
		want       bool
	}{
		{name: "same", start: base, end: base.Add(time.Hour), want: true},
		{name: "inside", start: base.Add(10 * time.Minute), end: base.Add(20 * time.Minute), want: true},
		{name: "overlaps start", start: base.Add(-time.Minute), end: base.Add(time.Minute), want: true},
		{name: "overlaps end", start: base.Add(59 * time.Minute), end: base.Add(61 * time.Minute), want: true},
		{name: "adjacent before", start: base.Add(-time.Hour), end: base, want: false},
		{name: "adjacent after", start: base.Add(time.Hour), end: base.Add(2 * time.Hour), want: false},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			other, err := worklog.NewInterval(test.start, test.end)
			if err != nil {
				t.Fatal(err)
			}
			if got := target.Overlaps(other); got != test.want {
				t.Errorf("Overlaps() = %v, want %v", got, test.want)
			}
		})
	}
}

func TestNewIntervalRejectsNonPositiveRange(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, time.July, 27, 9, 0, 0, 0, time.UTC)
	for _, end := range []time.Time{now, now.Add(-time.Second)} {
		if _, err := worklog.NewInterval(now, end); err == nil {
			t.Errorf("NewInterval(%v, %v) unexpectedly succeeded", now, end)
		}
	}
}
