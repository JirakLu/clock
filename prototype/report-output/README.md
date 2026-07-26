# Report output prototype

**Throwaway prototype:** this exists only to resolve
[Validate terminal and JSON report designs](https://github.com/JirakLu/clock/issues/7).
It is not production code.

The current direction combines:

- ledger-style context, headings, and totals for every human report;
- a detailed timeline for a single day;
- a compact, day-grouped Worklog ledger for a week;
- daily and per-issue aggregates for a month.

Run all three periods:

```sh
go run ./prototype/report-output/main.go
```

Inspect one period:

```sh
go run ./prototype/report-output/main.go --period day
go run ./prototype/report-output/main.go --period week
go run ./prototype/report-output/main.go --period month
```

Inspect the proposed stable JSON contract:

```sh
go run ./prototype/report-output/main.go --json
```

JSON always contains the full ordered contribution list, zero-duration daily
totals, and the window total. Its detail does not change with report duration;
only the terminal presentation becomes less verbose.
