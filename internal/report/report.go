package report

import (
	"errors"
	"sort"
	"strings"
	"time"

	"github.com/JirakLu/clock/internal/worklog"
)

type Selector uint8

const (
	Today Selector = iota + 1
	LastWeek
	LastMonth
	Explicit
)

func (s Selector) Value() string {
	switch s {
	case Today:
		return "today"
	case LastWeek:
		return "last-week"
	case LastMonth:
		return "last-month"
	case Explicit:
		return "explicit"
	default:
		return "unknown"
	}
}

func (s Selector) JSONType() string {
	if s == Explicit {
		return "explicit"
	}
	return "preset"
}

func (s Selector) JSONValue() string {
	return strings.ReplaceAll(s.Value(), "-", "_")
}

type Window struct {
	Selector Selector
	From     time.Time
	To       time.Time
	Timezone string
	location *time.Location
}

type LocalDate struct {
	midnight time.Time
}

func DateAt(value time.Time, location *time.Location) LocalDate {
	if location == nil {
		location = time.Local
	}
	local := value.In(location)
	return LocalDate{midnight: time.Date(
		local.Year(), local.Month(), local.Day(), 0, 0, 0, 0, location,
	)}
}

func (d LocalDate) String() string {
	return d.midnight.Format("2006-01-02")
}

func (d LocalDate) Format(layout string) string {
	return d.midnight.Format(layout)
}

func ResolvePreset(selector Selector, now time.Time, location *time.Location) (Window, error) {
	if location == nil {
		location = time.Local
	}
	localNow := now.In(location)
	today := time.Date(localNow.Year(), localNow.Month(), localNow.Day(), 0, 0, 0, 0, location)
	var from, to time.Time
	switch selector {
	case Today:
		from, to = today, today.AddDate(0, 0, 1)
	case LastWeek:
		daysSinceMonday := (int(today.Weekday()) + 6) % 7
		to = today.AddDate(0, 0, -daysSinceMonday)
		from = to.AddDate(0, 0, -7)
	case LastMonth:
		to = time.Date(today.Year(), today.Month(), 1, 0, 0, 0, 0, location)
		from = to.AddDate(0, -1, 0)
	default:
		return Window{}, errors.New("unsupported Report preset")
	}
	return NewWindow(selector, from, to, location)
}

func NewWindow(selector Selector, from, to time.Time, location *time.Location) (Window, error) {
	if !to.After(from) {
		return Window{}, errors.New("Report window --to must be after --from")
	}
	if location == nil {
		location = time.Local
	}
	return Window{
		Selector: selector,
		From:     from,
		To:       to,
		Timezone: location.String(),
		location: location,
	}, nil
}

func (w Window) Location() *time.Location {
	if w.location == nil {
		return time.Local
	}
	return w.location
}

type Contribution struct {
	WorklogID   string
	Issue       worklog.IssueKey
	Summary     string
	Description string
	From        time.Time
	To          time.Time
	Seconds     int64
	Date        LocalDate
}

type DailyTotal struct {
	Date    LocalDate
	Seconds int64
}

type Report struct {
	Window        Window
	Contributions []Contribution
	DailyTotals   []DailyTotal
	TotalSeconds  int64
}

func Build(window Window, worklogs []worklog.Worklog) Report {
	result := Report{Window: window}
	location := window.Location()
	dailyIndexes := make(map[string]int)
	localFrom := window.From.In(location)
	day := time.Date(localFrom.Year(), localFrom.Month(), localFrom.Day(), 0, 0, 0, 0, location)
	for day.Before(window.To) {
		date := DateAt(day, location)
		dailyIndexes[date.String()] = len(result.DailyTotals)
		result.DailyTotals = append(result.DailyTotals, DailyTotal{Date: date})
		day = day.AddDate(0, 0, 1)
	}

	for _, source := range worklogs {
		start := later(source.Interval.Start(), window.From)
		end := earlier(source.Interval.End(), window.To)
		if !end.After(start) {
			continue
		}
		for start.Before(end) {
			localStart := start.In(location)
			nextMidnight := time.Date(
				localStart.Year(), localStart.Month(), localStart.Day()+1,
				0, 0, 0, 0, location,
			)
			contributionEnd := earlier(end, nextMidnight)
			seconds := int64(contributionEnd.Sub(start) / time.Second)
			if seconds > 0 {
				date := DateAt(localStart, location)
				result.Contributions = append(result.Contributions, Contribution{
					WorklogID: source.ID, Issue: source.Issue, Summary: source.Summary,
					Description: source.Description, From: start, To: contributionEnd,
					Seconds: seconds, Date: date,
				})
				if index, ok := dailyIndexes[date.String()]; ok {
					result.DailyTotals[index].Seconds += seconds
				}
				result.TotalSeconds += seconds
			}
			start = contributionEnd
		}
	}
	sort.SliceStable(result.Contributions, func(left, right int) bool {
		a, b := result.Contributions[left], result.Contributions[right]
		if a.Date.String() != b.Date.String() {
			return a.Date.String() < b.Date.String()
		}
		if !a.From.Equal(b.From) {
			return a.From.Before(b.From)
		}
		return a.WorklogID < b.WorklogID
	})
	return result
}

func earlier(left, right time.Time) time.Time {
	if left.Before(right) {
		return left
	}
	return right
}

func later(left, right time.Time) time.Time {
	if left.After(right) {
		return left
	}
	return right
}
