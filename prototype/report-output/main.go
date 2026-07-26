// PROTOTYPE — throw this away after issue "Validate terminal and JSON report designs" is resolved.
//
// Question: Does this progressive terminal hierarchy feel right?
//
//	day   = full timeline detail
//	week  = compact contribution ledger
//	month = daily and issue summaries
//
// JSON deliberately keeps contribution-level detail for every period so scripts
// do not lose data merely because the human presentation becomes less verbose.
package main

import (
	"flag"
	"fmt"
	"os"
	"strings"
)

type reportCase struct {
	name     string
	terminal string
}

var reportCases = map[string]reportCase{
	"day": {
		name: "Single day — detailed timeline",
		terminal: `TODAY · 2026-07-26 00:00 → 2026-07-27 00:00 · Europe/Prague

SUN 26 JUL

09:00 ┌ CLK-7 — Report design
      │ Compare table layouts
10:30 └ 1h30m

10:00 ┌ OPS-42 — Deploy API
10:45 └ 45m

23:30 ┌ CLK-9 — Month close
      │ Reconcile July
24:00 └ 30m · continues tomorrow

                         DAY TOTAL  2h45m · 2,062.50 CZK

TOTAL  2h45m · 2,062.50 CZK

Note: overlapping Worklogs are counted independently.`,
	},
	"week": {
		name: "Week — compact daily ledger",
		terminal: `LAST WEEK · 2026-07-20 00:00 → 2026-07-27 00:00 · Europe/Prague

DATE        TIME         DURATION  ISSUE                  DESCRIPTION
Mon 20 Jul  09:00–10:30     1h30m  CLK-7 — Report design  Compare table layouts
            10:00–10:45       45m  OPS-42 — Deploy API    —
                                      DAY TOTAL            2h15m · 1,687.50 CZK
Tue 21 Jul  13:15–17:15        4h  CLK-7 — Report design  Settle JSON fields
                                      DAY TOTAL            4h · 3,000.00 CZK
Wed 22 Jul                            DAY TOTAL            0m · 0.00 CZK
Thu 23 Jul  23:30–24:00       30m  CLK-9 — Month close    Reconcile July
                                      DAY TOTAL            30m · 375.00 CZK
Fri 24 Jul  00:00–00:30       30m  CLK-9 — Month close    Reconcile July
                                      DAY TOTAL            30m · 375.00 CZK
Sat 25 Jul                            DAY TOTAL            0m · 0.00 CZK
Sun 26 Jul  09:00–11:00        2h  OPS-42 — Deploy API    Production rollout
                                      DAY TOTAL            2h · 1,500.00 CZK

TOTAL  9h15m · 6,937.50 CZK`,
	},
	"month": {
		name: "Month — aggregate summary",
		terminal: `LAST MONTH · 2026-06-01 00:00 → 2026-07-01 00:00 · Europe/Prague

DAILY TOTALS
DATE        DURATION       EARNINGS
Mon 01 Jun        6h   4,500.00 CZK
Tue 02 Jun     7h30m   5,625.00 CZK
Wed 03 Jun        0m       0.00 CZK
Thu 04 Jun        8h   6,000.00 CZK
Fri 05 Jun        4h   3,000.00 CZK
…                    …
Mon 29 Jun     7h15m   5,437.50 CZK
Tue 30 Jun        6h   4,500.00 CZK
(prototype abbreviated; the real report prints every date)

BY ISSUE
ISSUE                         DURATION
CLK-7 — Report design           62h15m
OPS-42 — Deploy API                48h
CLK-9 — Month close             35h30m

TOTAL  145h45m · 109,312.50 CZK

Use JSON output when individual Worklog contributions are needed.`,
	},
}

const jsonContract = `{
  "schema": "clock.report.v1",
  "selector": {
    "type": "preset",
    "value": "last_week"
  },
  "window": {
    "from": "2026-07-20T00:00:00+02:00",
    "to": "2026-07-27T00:00:00+02:00",
    "timezone": "Europe/Prague"
  },
  "contributions": [
    {
      "worklog_id": "10001",
      "issue": {
        "key": "CLK-7",
        "summary": "Report design"
      },
      "description": "Compare table layouts",
      "from": "2026-07-20T09:00:00+02:00",
      "to": "2026-07-20T10:30:00+02:00",
      "seconds": 5400
    },
    {
      "worklog_id": "10002",
      "issue": {
        "key": "OPS-42",
        "summary": "Deploy API"
      },
      "from": "2026-07-20T10:00:00+02:00",
      "to": "2026-07-20T10:45:00+02:00",
      "seconds": 2700
    },
    {
      "worklog_id": "10004",
      "issue": {
        "key": "CLK-7",
        "summary": "Report design"
      },
      "description": "Settle JSON fields",
      "from": "2026-07-21T13:15:00+02:00",
      "to": "2026-07-21T17:15:00+02:00",
      "seconds": 14400
    },
    {
      "worklog_id": "10003",
      "issue": {
        "key": "CLK-9",
        "summary": "Month close"
      },
      "description": "Reconcile July",
      "from": "2026-07-23T23:30:00+02:00",
      "to": "2026-07-24T00:00:00+02:00",
      "seconds": 1800
    },
    {
      "worklog_id": "10003",
      "issue": {
        "key": "CLK-9",
        "summary": "Month close"
      },
      "description": "Reconcile July",
      "from": "2026-07-24T00:00:00+02:00",
      "to": "2026-07-24T00:30:00+02:00",
      "seconds": 1800
    },
    {
      "worklog_id": "10005",
      "issue": {
        "key": "OPS-42",
        "summary": "Deploy API"
      },
      "description": "Production rollout",
      "from": "2026-07-26T09:00:00+02:00",
      "to": "2026-07-26T11:00:00+02:00",
      "seconds": 7200
    }
  ],
  "daily_totals": [
    {
      "date": "2026-07-20",
      "seconds": 8100,
      "earnings_czk": "1687.50"
    },
    {
      "date": "2026-07-21",
      "seconds": 14400,
      "earnings_czk": "3000.00"
    },
    {
      "date": "2026-07-22",
      "seconds": 0,
      "earnings_czk": "0.00"
    },
    {
      "date": "2026-07-23",
      "seconds": 1800,
      "earnings_czk": "375.00"
    },
    {
      "date": "2026-07-24",
      "seconds": 1800,
      "earnings_czk": "375.00"
    },
    {
      "date": "2026-07-25",
      "seconds": 0,
      "earnings_czk": "0.00"
    },
    {
      "date": "2026-07-26",
      "seconds": 7200,
      "earnings_czk": "1500.00"
    }
  ],
  "total": {
    "seconds": 33300,
    "earnings_czk": "6937.50"
  }
}`

func main() {
	period := flag.String("period", "", "day, week, or month (empty prints all)")
	asJSON := flag.Bool("json", false, "print the stable JSON contract example")
	flag.Parse()

	if *asJSON {
		fmt.Println(jsonContract)
		return
	}

	if *period != "" {
		printCase(*period)
		return
	}

	for index, key := range []string{"day", "week", "month"} {
		if index > 0 {
			fmt.Println("\n" + strings.Repeat("=", 72) + "\n")
		}
		printCase(key)
	}
}

func printCase(key string) {
	value, ok := reportCases[key]
	if !ok {
		fmt.Fprintf(os.Stderr, "unknown period %q; use day, week, or month\n", key)
		os.Exit(2)
	}

	fmt.Println(value.name)
	fmt.Println(strings.Repeat("-", len(value.name)))
	fmt.Println(value.terminal)
}
