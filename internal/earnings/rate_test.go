package earnings_test

import (
	"testing"

	"github.com/JirakLu/clock/internal/earnings"
)

func TestParseHourlyRatePreservesExactQuotedAmount(t *testing.T) {
	t.Parallel()

	tests := []struct {
		input  string
		halere int64
	}{
		{input: "0", halere: 0},
		{input: "0.5", halere: 50},
		{input: "750.00", halere: 75_000},
		{input: "001.20", halere: 120},
	}

	for _, test := range tests {
		test := test
		t.Run(test.input, func(t *testing.T) {
			t.Parallel()

			rate, err := earnings.ParseHourlyRate(test.input)
			if err != nil {
				t.Fatalf("ParseHourlyRate() error = %v", err)
			}
			if got := rate.QuotedCZK(); got != test.input {
				t.Errorf("QuotedCZK() = %q, want %q", got, test.input)
			}
			if got := rate.Halere(); got != test.halere {
				t.Errorf("Halere() = %d, want %d", got, test.halere)
			}
		})
	}
}

func TestParseHourlyRateRejectsInvalidAmounts(t *testing.T) {
	t.Parallel()

	for _, input := range []string{"", "-1", "+1", "1.", ".5", "1.234", "1,20", " 1.20", "NaN"} {
		input := input
		t.Run(input, func(t *testing.T) {
			t.Parallel()
			if _, err := earnings.ParseHourlyRate(input); err == nil {
				t.Fatalf("ParseHourlyRate(%q) unexpectedly succeeded", input)
			}
		})
	}
}
