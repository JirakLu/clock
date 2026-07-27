# PROTOTYPE — `clock` v1 acceptance specification

> Throwaway review artifact for
> [Validate the final v1 acceptance specification](https://github.com/JirakLu/clock/issues/11).
> It is not an implementation document until the open validation point is accepted.

## Purpose

This is the handoff contract for implementing `clock` v1. An implementation is
acceptable when:

1. every scenario in this document passes at its assigned verification layer;
2. every architecture obligation is visible in the implementation;
3. every release gate passes against the exact artifacts to be published; and
4. no behavior contradicts a linked Wayfinder decision.

The linked decision tickets remain authoritative for rationale and edge-case detail.
This document converts them into traceable, externally observable success criteria
without copying their full resolutions.

## Open validation point

The resolved decisions define report semantics and output, but not the exact report
command grammar. This prototype proposes:

```text
clock report today [--earnings] [--json]
clock report last-week [--earnings] [--json]
clock report last-month [--earnings] [--json]
clock report --from <bound> --to <bound> [--earnings] [--json]
```

- `--from` and `--to` are required together and conflict with presets.
- A bound is a local `YYYY-MM-DD`, local `YYYY-MM-DDTHH:MM`, or an
  offset-bearing `YYYY-MM-DDTHH:MM+02:00`.
- A date means local midnight. An offset-less ambiguous daylight-saving time fails
  and asks for an explicit offset.
- `--earnings` adds CZK Earnings to terminal and JSON aggregates.
- `--json` selects `clock.report.v1`; without it, terminal output is used.

Acceptance scenarios below use this grammar provisionally.

## Authoritative decisions

| Area | Source |
| --- | --- |
| Runtime and CLI ecosystem | [Choose the implementation language and CLI ecosystem](https://github.com/JirakLu/clock/issues/3) |
| Jira API | [Establish the Jira Cloud integration contract](https://github.com/JirakLu/clock/issues/2) |
| Commands and Running timer | [Define the command grammar and timer lifecycle](https://github.com/JirakLu/clock/issues/4) |
| Report and Earnings semantics | [Define worklog reporting and earnings semantics](https://github.com/JirakLu/clock/issues/5) |
| Configuration and local state | [Choose configuration, credentials, and timer persistence](https://github.com/JirakLu/clock/issues/6) |
| Terminal and JSON reports | [Validate terminal and JSON report designs](https://github.com/JirakLu/clock/issues/7) |
| Invalid timer recovery | [Decide recovery from stale or corrupted timer state](https://github.com/JirakLu/clock/issues/8) |
| Module boundaries | [Design extensible module boundaries](https://github.com/JirakLu/clock/issues/9) |
| Verification and release | [Choose verification, packaging, and release workflow](https://github.com/JirakLu/clock/issues/10) |

## Compatibility surface

V1 treats these observable contracts as compatibility-sensitive:

- commands, flags, accepted input, conflicts, stdout/stderr, and exit behavior;
- `clock.report.v1`;
- `config.toml` and Running timer `state.json` schema version 1;
- credential lookup identity and the absence of plaintext credential fallbacks;
- Jira interval, overlap, pagination, author-filtering, and write-outcome behavior;
- report selection, clipping, splitting, ordering, totals, and Earnings rounding; and
- archive names, supported platforms, checksums, `--version`, and release notes.

## Acceptance scenarios

Each scenario names the lowest layer that must prove the behavior:

- **Domain** — pure table-driven domain test.
- **Application** — typed capability test with fixed time and small in-memory ports.
- **Adapter** — real HTTP, keyring-adapter, or temporary-directory contract test.
- **CLI** — in-process fresh Cobra root with injected streams and dependencies.
- **Binary** — compiled executable or extracted release artifact.
- **Live** — dedicated disposable Jira Cloud site/project.

### Configuration and identity

#### CFG-01 — First configuration succeeds atomically

**Given** no existing configuration, a usable OS credential store, and valid Jira
Cloud site, email, scoped API token, and non-negative CZK Hourly rate  
**When** the user completes `clock configure` and confirms the discovered site and
Jira identity  
**Then** `config.toml` contains the canonical site URL, Cloud ID, email, account ID,
and exact quoted Hourly rate; the token exists only in the native credential store
under the Cloud-ID/account-ID key; and no token appears in files or output.

Verification: **Application + Adapter + CLI**  
Source: [Choose configuration, credentials, and timer persistence](https://github.com/JirakLu/clock/issues/6)

#### CFG-02 — Failed validation does not replace working configuration

**Given** an existing valid configuration and credential  
**When** a configure rerun fails site discovery, `/myself`, confirmation, keyring
access, or configuration persistence  
**Then** the command exits non-zero with an actionable error and does not present
the new identity as configured.

Verification: **Application + Adapter + CLI**  
Source: [Choose configuration, credentials, and timer persistence](https://github.com/JirakLu/clock/issues/6)

#### CFG-03 — Direct edits remain fail-closed

**Given** malformed TOML, an invalid Hourly rate, a locked or unavailable credential
store, or a configured account ID that differs from `/myself`  
**When** an affected Jira command runs  
**Then** it exits non-zero with a path- or identity-specific diagnosis, does not
silently replace configuration, and does not contact Jira for a Worklog mutation.

Verification: **Application + Adapter + CLI**  
Sources: [Choose configuration, credentials, and timer persistence](https://github.com/JirakLu/clock/issues/6), [Establish the Jira Cloud integration contract](https://github.com/JirakLu/clock/issues/2)

### Manual Worklogs

#### LOG-01 — Accepted manual forms create the intended Worklog

**Given** an authenticated user and no authored Worklog overlap  
**When** the user logs a normalized Jira issue key with a positive compact
hours/minutes Duration, optionally supplying `--at` and a description  
**Then** exactly one Worklog is created with the derived start, exact positive
seconds, `adjustEstimate=leave`, and an ADF comment only when a description exists.

Verification: **Domain + Application + Adapter + CLI**  
Sources: [Define the command grammar and timer lifecycle](https://github.com/JirakLu/clock/issues/4), [Establish the Jira Cloud integration contract](https://github.com/JirakLu/clock/issues/2)

#### LOG-02 — `--after-last` infers one valid interval

**Given** today's latest accessible authored Worklog ends before now  
**When** the user runs `clock log <issue> --after-last`  
**Then** the new interval begins at that Worklog's end and ends now.

**And given** there is no eligible Worklog, or the latest end is now or later  
**When** the same command runs  
**Then** it fails without mutation and suggests `--at` or the default form.

Verification: **Application + CLI**  
Source: [Define the command grammar and timer lifecycle](https://github.com/JirakLu/clock/issues/4)

#### LOG-03 — Invalid or overlapping input never writes

**Given** an invalid issue key, Duration, timestamp, future end, non-positive
interval, ambiguous local DST time, conflicting flags, or authored Worklog overlap  
**When** `clock log` runs  
**Then** it exits non-zero before any Jira Worklog write and identifies the
correctable input or overlap.

Verification: **Domain + Application + CLI**  
Source: [Define the command grammar and timer lifecycle](https://github.com/JirakLu/clock/issues/4)

#### LOG-04 — Write failure preserves manual recovery facts

**Given** a valid manual Worklog request  
**When** Jira definitely rejects it or returns an uncertain outcome  
**Then** the command exits non-zero and prints the issue, start, end, Duration, and
description; uncertain outcomes additionally tell the user to check Jira before
retrying; no retry or failed submission is retained.

Verification: **Application + Adapter + CLI**  
Sources: [Define the command grammar and timer lifecycle](https://github.com/JirakLu/clock/issues/4), [Design extensible module boundaries](https://github.com/JirakLu/clock/issues/9)

### Running timer

#### TMR-01 — Start, inspect, and discard one timer

**Given** no Running timer  
**When** `clock start` uses now, `--at`, or `--after-last` with valid non-overlapping
input  
**Then** one identity-bound Running timer is atomically stored.

**When** `clock status` runs  
**Then** it displays the issue, start, elapsed Duration, and optional description.

**When** `clock discard` runs  
**Then** it removes the valid timer without contacting Jira and reports what was
discarded.

Verification: **Domain + Application + Adapter + CLI**  
Sources: [Define the command grammar and timer lifecycle](https://github.com/JirakLu/clock/issues/4), [Choose configuration, credentials, and timer persistence](https://github.com/JirakLu/clock/issues/6)

#### TMR-02 — A second timer is rejected without mutation

**Given** a Running timer already exists  
**When** any `clock start` form runs  
**Then** it exits non-zero, shows the active timer, directs the user to `stop` or
`discard`, and leaves state unchanged.

Verification: **Application + CLI**  
Source: [Define the command grammar and timer lifecycle](https://github.com/JirakLu/clock/issues/4)

#### TMR-03 — Stop creates one completed Worklog

**Given** a valid Running timer and no overlap at stop time  
**When** `clock stop`, optionally with a past `--at` and description override, runs  
**Then** it truncates elapsed time to whole seconds, rejects a result below one
second, consumes timer state before the Jira request, and creates exactly one
completed Worklog on success.

Verification: **Domain + Application + Adapter + CLI**  
Sources: [Define the command grammar and timer lifecycle](https://github.com/JirakLu/clock/issues/4), [Decide recovery from stale or corrupted timer state](https://github.com/JirakLu/clock/issues/8)

#### TMR-04 — Pre-write stop failure preserves the timer

**Given** a Running timer  
**When** stop input, identity binding, or the repeated Jira overlap check fails  
**Then** the command exits non-zero without a Jira write and leaves the Running
timer intact.

Verification: **Application + Adapter + CLI**  
Sources: [Define the command grammar and timer lifecycle](https://github.com/JirakLu/clock/issues/4), [Choose configuration, credentials, and timer persistence](https://github.com/JirakLu/clock/issues/6)

#### TMR-05 — Post-consumption failure is recoverable only by the user

**Given** a valid stop has consumed local state  
**When** Jira definitely rejects the write, its outcome is uncertain, or the process
crashes  
**Then** no Running timer or retry record is recreated; known details are reported
when the process can do so; and an uncertain outcome tells the user to check Jira
before manual recovery.

Verification: **Application + Adapter + Binary**  
Sources: [Define the command grammar and timer lifecycle](https://github.com/JirakLu/clock/issues/4), [Decide recovery from stale or corrupted timer state](https://github.com/JirakLu/clock/issues/8)

#### TMR-06 — Invalid or incomplete state fails closed

**Given** malformed, unsupported, structurally invalid, future-dated, or incomplete
atomic timer state  
**When** `status`, `start`, `stop`, or ordinary `discard` runs  
**Then** it exits non-zero with the state path, validation reason, and
`clock discard --force` guidance, without treating the state as absent.

**When** independent `log`, `report`, or `configure` runs  
**Then** invalid timer state does not block it.

Verification: **Adapter + CLI**  
Source: [Decide recovery from stale or corrupted timer state](https://github.com/JirakLu/clock/issues/8)

#### TMR-07 — Forced discard removes every timer artifact or reports failure

**Given** invalid canonical or staging timer artifacts  
**When** `clock discard --force` runs  
**Then** it never contacts Jira, removes every timer artifact it can, names removed
paths, states that no Worklog was created, and exits non-zero if nothing existed or
any required removal failed.

Verification: **Adapter + CLI**  
Source: [Decide recovery from stale or corrupted timer state](https://github.com/JirakLu/clock/issues/8)

### Jira reads

#### JRA-01 — Reports retrieve all accessible authored Worklogs

**Given** paginated Jira results, candidate issues with more Worklogs than embedded
in search, Worklogs by other authors, and permission-limited projects  
**When** a report loads its widened candidate range  
**Then** it pages candidate issues and each issue's Worklogs, retains only the
configured account ID, applies exact instant filtering client-side, and makes no
claim beyond authored Worklogs the account can access.

Verification: **Adapter + Live**  
Source: [Establish the Jira Cloud integration contract](https://github.com/JirakLu/clock/issues/2)

#### JRA-02 — Jira protocol and error classification are stable

**Given** rate limits, permission failures, malformed responses, timeouts, and
connection loss around Worklog creation  
**When** the Jira adapter handles them  
**Then** it follows REST v3 pagination/mapping and returns typed errors, including
definite-rejection versus uncertain-outcome classification without text matching.

Verification: **Adapter**  
Sources: [Establish the Jira Cloud integration contract](https://github.com/JirakLu/clock/issues/2), [Design extensible module boundaries](https://github.com/JirakLu/clock/issues/9)

### Reports and Earnings

#### RPT-01 — Presets resolve to concrete local half-open windows

**Given** a fixed current time and machine IANA timezone  
**When** `today`, `last-week`, or `last-month` is requested  
**Then** the Report window is respectively the current local day, previous
Monday-through-Sunday week, or previous calendar month; output identifies the
selector, concrete offset-bearing `[from, to)` bounds, and timezone.

Verification: **Domain + Application + CLI**  
Source: [Define worklog reporting and earnings semantics](https://github.com/JirakLu/clock/issues/5)

#### RPT-02 — Explicit windows reject invalid bounds

**Given** explicit local or offset-bearing bounds  
**When** both resolve to an ordered half-open window  
**Then** the report uses those exact instants.

**And given** a missing bound, a non-increasing window, or an ambiguous offset-less
DST time  
**When** the report runs  
**Then** it fails without producing a partial report.

Verification: **Domain + Application + CLI**  
Sources: [Define worklog reporting and earnings semantics](https://github.com/JirakLu/clock/issues/5), proposed report grammar

#### RPT-03 — Selection, clipping, and local-day splitting preserve identity

**Given** authored Worklogs outside, inside, crossing, and overlapping the Report
window and local-midnight boundaries, including a daylight-saving transition  
**When** reporting runs  
**Then** it selects only positive overlaps, clips to the window, splits at each
local midnight, measures actual elapsed seconds, and retains the original Jira
Worklog ID, issue, summary, and optional description on every contribution.

Verification: **Domain + Application**  
Source: [Define worklog reporting and earnings semantics](https://github.com/JirakLu/clock/issues/5)

#### RPT-04 — Ordering and totals are deterministic

**Given** contributions with overlaps, exact start ties, adjacent endpoints, and
days without work  
**When** the Report is assembled  
**Then** dates and effective starts ascend, Worklog ID breaks exact ties, overlapping
contributions count independently, every window date has a daily total including
zero, and the exact whole-window seconds equal the sum of contributions.

Verification: **Domain**  
Source: [Define worklog reporting and earnings semantics](https://github.com/JirakLu/clock/issues/5)

#### RPT-05 — Earnings use exact aggregate seconds

**Given** a configured exact Hourly rate in haléře  
**When** `--earnings` is requested for current or historical Worklogs  
**Then** each daily and window Earnings value is calculated from its aggregate exact
seconds, rounded half-up to two decimal CZK only for display, never summed from
rounded row values, and never persisted.

Verification: **Domain + CLI**  
Sources: [Define worklog reporting and earnings semantics](https://github.com/JirakLu/clock/issues/5), [Choose configuration, credentials, and timer persistence](https://github.com/JirakLu/clock/issues/6)

#### RPT-06 — Terminal detail decreases with window size

**Given** representative day, week, and month Reports  
**When** terminal presentation is selected  
**Then** a day shows a detailed contribution timeline, a week shows a date-grouped
contribution ledger, and a month shows daily totals plus issue summary without
individual rows; all include the resolved window and exact Duration total, and
Earnings only when requested.

Verification: **CLI golden fixtures**  
Source: [Validate terminal and JSON report designs](https://github.com/JirakLu/clock/issues/7)

#### RPT-07 — JSON retains full detail for every window

**Given** any Report length  
**When** `--json` is selected  
**Then** output validates as `clock.report.v1`, contains selector and resolved window,
all ordered contributions, every daily total, and the window total; seconds are
integers, CZK values are two-decimal strings when requested, and absent descriptions
are omitted rather than `null`.

Verification: **CLI golden fixtures + schema/DTO contract test**  
Source: [Validate terminal and JSON report designs](https://github.com/JirakLu/clock/issues/7)

### CLI process contract

#### CLI-01 — Streams and exit behavior are testable and consistent

**Given** success, usage failure, validation failure, definite Jira rejection,
uncertain Jira outcome, and local persistence failure  
**When** the corresponding command runs  
**Then** structured success output goes to stdout, diagnostics go to stderr, success
returns zero, and every failure returns non-zero without application modules
printing directly.

Verification: **CLI + Binary**  
Sources: [Design extensible module boundaries](https://github.com/JirakLu/clock/issues/9), [Choose verification, packaging, and release workflow](https://github.com/JirakLu/clock/issues/10)

#### CLI-02 — Help and version expose the shipped contract

**Given** a built artifact  
**When** help or `clock --version` runs  
**Then** help documents the accepted grammar and conflicts, and version reports the
SemVer version plus source revision without a build timestamp.

Verification: **CLI + Binary**  
Source: [Choose verification, packaging, and release workflow](https://github.com/JirakLu/clock/issues/10)

## Architecture obligations

Implementation review must confirm:

- `cmd/clock` is only the composition root; all v1 packages remain under `internal/`.
- Cobra, rendered strings, JSON tags, HTTP, TOML, keyring, and filesystem formats do
  not leak into domain or application capability interfaces.
- `configure`, `log`, `timer`, and `report` expose typed application operations.
- Manual log and timer stop share duplicate-sensitive interval validation,
  overlap, submission, and Jira-outcome classification through `recording`.
- `reporting` and `earnings` remain pure; current time and local timezone are
  injected rather than read inside domain logic.
- Jira is one deep HTTP adapter satisfying small consumer-owned ports.
- Configuration and timer-state persistence are deep concrete modules; only the
  credential store is a replaceable persistence port.
- Timer-state operations express create, inspect/diagnose, consume, discard, and
  force-discard rather than generic file access.
- Terminal and `clock.report.v1` render from structured results through separate
  presenters; application code does not print.
- No speculative seams are added for OAuth, multiple timers, other trackers,
  historical rates, currencies, or platforms.

Source: [Design extensible module boundaries](https://github.com/JirakLu/clock/issues/9)

## Verification gates

### Every change

- Go formatting and vetting pass.
- Domain and application tests pass with fixed time.
- Jira HTTP, configuration, and timer-state contract tests pass.
- Fresh-root Cobra tests pass.
- Linux/amd64 race detection passes.
- Cross-builds succeed with `CGO_ENABLED=0` for Linux and macOS on amd64 and arm64.

### Main

- Every-change checks pass.
- Native Linux and macOS compiled-binary smoke tests pass.

### Release candidate or release tag

- Main checks pass.
- The serialized live Jira suite validates identity, queries a known issue, creates
  a uniquely marked Worklog, reads it back, and cleans it up without exposing
  credentials or blindly retrying uncertain creation.
- The exact extracted archives pass native smoke tests on matching runners.
- `clock --version` reports the tag's SemVer and source revision.
- Four versioned `.tar.gz` archives and one SHA-256 checksum file are ready.
- Release notes disclose that macOS artifacts are unsigned.

Source: [Choose verification, packaging, and release workflow](https://github.com/JirakLu/clock/issues/10)

## Handoff package

The implementation handoff consists of:

1. this accepted specification as the acceptance and compatibility index;
2. the nine linked decision resolutions as authoritative detail and rationale;
3. the Jira and verification research branches linked from their decision tickets;
4. the validated report-output prototype linked from
   [Validate terminal and JSON report designs](https://github.com/JirakLu/clock/issues/7);
5. `CONTEXT.md` as the canonical domain vocabulary; and
6. an implementation issue or plan whose work items cite scenario IDs and preserve
   the verification-layer assignments.

An implementation work item is not complete merely because code exists. It is
complete when its cited scenarios pass, its linked architecture obligations hold,
and the next applicable verification gate remains green.

## Out of scope

- Implementing the CLI in this Wayfinder map.
- Jira Data Center or Server; Windows; OAuth or multi-user onboarding.
- More than one Running timer.
- Editing or deleting Worklogs.
- Offline queues, automatic retries, or retained failed submissions.
- Historical Hourly rates, persisted Earnings, other currencies, or conversion.
- Jira-style day/week Duration input.
- Homebrew, signing, and notarization for v1.
