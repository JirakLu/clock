package cli

import (
	"errors"
	"fmt"
	"time"
)

func parseMinuteTimestamp(raw string, now time.Time, location *time.Location) (time.Time, error) {
	if location == nil {
		location = time.Local
	}
	if parsed, err := time.Parse("2006-01-02T15:04Z07:00", raw); err == nil {
		return parsed, nil
	}

	var wall time.Time
	var err error
	switch len(raw) {
	case len("15:04"):
		wall, err = time.Parse("15:04", raw)
		if err == nil {
			return resolveLocalMinute(
				now.In(location).Year(),
				now.In(location).Month(),
				now.In(location).Day(),
				wall.Hour(),
				wall.Minute(),
				location,
			)
		}
	case len("2006-01-02T15:04"):
		wall, err = time.Parse("2006-01-02T15:04", raw)
		if err == nil {
			return resolveLocalMinute(
				wall.Year(), wall.Month(), wall.Day(), wall.Hour(), wall.Minute(), location,
			)
		}
	}
	return time.Time{}, fmt.Errorf(
		`timestamp %q must be minute-precise HH:MM, YYYY-MM-DDTHH:MM, or YYYY-MM-DDTHH:MM+02:00`,
		raw,
	)
}

func resolveLocalMinute(
	year int,
	month time.Month,
	day int,
	hour int,
	minute int,
	location *time.Location,
) (time.Time, error) {
	naiveUTC := time.Date(year, month, day, hour, minute, 0, 0, time.UTC)
	var matches []time.Time
	for candidate := naiveUTC.Add(-15 * time.Hour); !candidate.After(naiveUTC.Add(15 * time.Hour)); candidate = candidate.Add(time.Minute) {
		local := candidate.In(location)
		if local.Year() == year && local.Month() == month && local.Day() == day &&
			local.Hour() == hour && local.Minute() == minute && local.Second() == 0 {
			matches = append(matches, local)
		}
	}
	switch len(matches) {
	case 1:
		return matches[0], nil
	case 0:
		return time.Time{}, errors.New(
			"local timestamp does not exist because of a daylight-saving transition; provide an explicit offset",
		)
	default:
		return time.Time{}, errors.New(
			"local timestamp is ambiguous because of a daylight-saving transition; provide an explicit offset",
		)
	}
}
