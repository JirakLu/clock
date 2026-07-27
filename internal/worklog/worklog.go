package worklog

import (
	"errors"
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/JirakLu/clock/internal/jiraidentity"
)

var (
	issueKeyPattern = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9_]*-[0-9]+$`)
	durationPattern = regexp.MustCompile(`^(?:([0-9]+)h)?(?:([0-9]+)m)?$`)
)

type IssueKey string

func ParseIssueKey(raw string) (IssueKey, error) {
	if !issueKeyPattern.MatchString(raw) {
		return "", fmt.Errorf("%q is not a Jira issue key (expected a key such as CLOCK-13)", raw)
	}
	return IssueKey(strings.ToUpper(raw)), nil
}

func (k IssueKey) String() string {
	return string(k)
}

type Duration struct {
	seconds int64
}

func ParseCompactDuration(raw string) (Duration, error) {
	match := durationPattern.FindStringSubmatch(raw)
	if match == nil || (match[1] == "" && match[2] == "") {
		return Duration{}, fmt.Errorf(
			"Duration %q must use positive compact hours and minutes, such as 30m, 2h, or 2h30m",
			raw,
		)
	}
	hours, err := parsePart(match[1])
	if err != nil {
		return Duration{}, fmt.Errorf("Duration %q is too large", raw)
	}
	minutes, err := parsePart(match[2])
	if err != nil {
		return Duration{}, fmt.Errorf("Duration %q is too large", raw)
	}
	const maxSeconds = int64((1<<63 - 1) / int64(time.Second))
	if hours > uint64(maxSeconds/3600) ||
		minutes > uint64(maxSeconds/60) ||
		int64(hours)*3600 > maxSeconds-int64(minutes)*60 {
		return Duration{}, fmt.Errorf("Duration %q is too large", raw)
	}
	seconds := int64(hours)*3600 + int64(minutes)*60
	if seconds <= 0 {
		return Duration{}, errors.New("Duration must be positive")
	}
	return Duration{seconds: seconds}, nil
}

func parsePart(raw string) (uint64, error) {
	if raw == "" {
		return 0, nil
	}
	return strconv.ParseUint(raw, 10, 64)
}

func DurationFromSeconds(seconds int64) (Duration, error) {
	if seconds <= 0 {
		return Duration{}, errors.New("Duration must be positive")
	}
	return Duration{seconds: seconds}, nil
}

func (d Duration) Seconds() int64 {
	return d.seconds
}

func (d Duration) Valid() bool {
	return d.seconds > 0
}

func (d Duration) Duration() time.Duration {
	return time.Duration(d.seconds) * time.Second
}

func (d Duration) String() string {
	hours := d.seconds / 3600
	minutes := (d.seconds % 3600) / 60
	seconds := d.seconds % 60
	switch {
	case seconds != 0 && hours != 0:
		return fmt.Sprintf("%dh%dm%ds", hours, minutes, seconds)
	case seconds != 0 && minutes != 0:
		return fmt.Sprintf("%dm%ds", minutes, seconds)
	case seconds != 0:
		return fmt.Sprintf("%ds", seconds)
	case hours != 0 && minutes != 0:
		return fmt.Sprintf("%dh%dm", hours, minutes)
	case hours != 0:
		return fmt.Sprintf("%dh", hours)
	default:
		return fmt.Sprintf("%dm", minutes)
	}
}

type Interval struct {
	start time.Time
	end   time.Time
}

func NewInterval(start, end time.Time) (Interval, error) {
	if !end.After(start) {
		return Interval{}, errors.New("Worklog interval must have a positive Duration")
	}
	return Interval{start: start, end: end}, nil
}

func (i Interval) Start() time.Time {
	return i.start
}

func (i Interval) End() time.Time {
	return i.end
}

func (i Interval) Seconds() int64 {
	return int64(i.end.Sub(i.start) / time.Second)
}

func (i Interval) Overlaps(other Interval) bool {
	return i.start.Before(other.end) && other.start.Before(i.end)
}

type Worklog struct {
	ID          string
	Issue       IssueKey
	Summary     string
	AuthorID    jiraidentity.AccountID
	Interval    Interval
	Description string
}

type Draft struct {
	Issue       IssueKey
	Interval    Interval
	Description string
}
