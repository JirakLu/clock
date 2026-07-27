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
