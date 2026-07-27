package earnings

import (
	"fmt"
	"math"
	"strconv"
	"strings"
)

// HourlyRate is an exact non-negative amount of CZK per hour.
type HourlyRate struct {
	quotedCZK string
	halere    int64
}

func ParseHourlyRate(input string) (HourlyRate, error) {
	if input == "" || strings.TrimSpace(input) != input {
		return HourlyRate{}, fmt.Errorf("Hourly rate must be a non-negative CZK amount with at most two decimal places")
	}

	parts := strings.Split(input, ".")
	if len(parts) > 2 || parts[0] == "" || !decimalDigits(parts[0]) {
		return HourlyRate{}, fmt.Errorf("Hourly rate %q must be a non-negative CZK amount with at most two decimal places", input)
	}
	fraction := ""
	if len(parts) == 2 {
		fraction = parts[1]
		if fraction == "" || len(fraction) > 2 || !decimalDigits(fraction) {
			return HourlyRate{}, fmt.Errorf("Hourly rate %q must have at most two decimal places", input)
		}
	}

	whole, err := strconv.ParseInt(parts[0], 10, 64)
	if err != nil || whole > math.MaxInt64/100 {
		return HourlyRate{}, fmt.Errorf("Hourly rate %q is too large", input)
	}
	halere := whole * 100
	switch len(fraction) {
	case 1:
		halere += int64(fraction[0]-'0') * 10
	case 2:
		halere += int64(fraction[0]-'0')*10 + int64(fraction[1]-'0')
	}

	return HourlyRate{quotedCZK: input, halere: halere}, nil
}

func (r HourlyRate) QuotedCZK() string {
	return r.quotedCZK
}

func (r HourlyRate) Halere() int64 {
	return r.halere
}

func (r HourlyRate) Valid() bool {
	return r.quotedCZK != ""
}

func decimalDigits(value string) bool {
	for _, char := range value {
		if char < '0' || char > '9' {
			return false
		}
	}
	return true
}
