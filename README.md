# clock

A personal CLI for time tracking in Jira Cloud.

## Build

Requires Go 1.24 or newer:

```sh
go build -o clock ./cmd/clock
```

Release builds inject a SemVer version and source revision without a build
timestamp:

```sh
go build \
  -ldflags "-X main.version=v1.0.0 -X main.revision=$(git rev-parse --short HEAD)" \
  -o clock ./cmd/clock
```

## Configure

```text
clock configure
```

The interactive flow asks for the Jira Cloud site URL, Atlassian email, exact CZK
Hourly rate, and a hidden API token. It discovers the Jira Cloud ID, validates
the authenticated Jira identity through `/myself`, and asks for confirmation.

Non-secret settings are atomically stored in the platform configuration
directory at `clock/config.toml`. The API token is stored only in macOS Keychain
or Linux Secret Service, bound to the validated Cloud ID and Jira account ID.
There is no command-line, environment-variable, or plaintext-file token fallback.

## Report

```text
clock report today [--earnings] [--json]
clock report last-week [--earnings] [--json]
clock report last-month [--earnings] [--json]
clock report --from <bound> --to <bound> [--earnings] [--json]
```

Reports include accessible Worklogs authored by the configured Jira identity.
Explicit bounds form a half-open `[from, to)` window and accept a local date,
local minute-precise timestamp, or offset-bearing minute-precise timestamp.
Terminal detail decreases from daily timelines to weekly ledgers and monthly
aggregates; `--json` always emits the fully detailed `clock.report.v1` contract.

## Running timer

```text
clock start <issue> [--at <start> | --after-last] [-d|--description <text>]
clock status
clock stop [--at <stop>] [-d|--description <text>]
clock discard [--force]
```

One identity-bound Running timer is stored locally at a time. Stopping validates
authored-Worklog overlap again, consumes local state before Jira submission, and
preserves exact elapsed whole seconds. Discard never contacts Jira.

Invalid or incomplete local timer state fails closed and reports its path,
reason, and recovery command. `clock discard --force` removes invalid canonical
and staging artifacts without contacting Jira; it refuses to remove valid
Running timer state. A valid Running timer never expires because of its age or
file modification time.
