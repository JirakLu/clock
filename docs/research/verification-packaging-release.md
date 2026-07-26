# Verification, packaging, and release

Research date: 2026-07-26

## Recommendation

Use four verification layers, with most tests running locally and deterministically:

1. pure domain and application tests against small injected interfaces;
2. HTTP and persistence adapter contract tests;
3. in-process Cobra command tests; and
4. a narrow compiled-binary smoke suite.

Test Jira behavior against a local HTTP server on every change and reserve a dedicated Jira Cloud developer site for manual or release-gated integration tests. Build self-contained pure-Go executables for Linux and macOS on amd64 and arm64, execute smoke tests natively, and publish versioned archives plus SHA-256 checksums through GitHub Releases. Direct downloads are the baseline installation channel; Homebrew is a later convenience channel. Because v1 is a personal tool rather than a multi-user distribution, macOS Developer ID signing and notarization are not release requirements; the release notes must state that the macOS artifact is unsigned.

## Test layers

### Domain and application tests

Test domain rules and application capabilities through consumer-owned interfaces with fixed clocks and in-memory adapters. Table-driven cases should cover successful behavior, boundaries, and typed failures. Go's standard tooling recognizes `_test.go` files and `Test...` functions, and the Go project documents table-driven tests as a compact way to cover repeated input/output cases ([Go testing tutorial](https://go.dev/doc/tutorial/add-a-test), [table-driven tests](https://go.dev/wiki/TableDrivenTests)).

This layer is fast and diagnostic, but it cannot detect HTTP serialization, Cobra wiring, process exit codes, or packaging defects.

### Adapter contract tests

Exercise the real Jira HTTP adapter against `httptest.NewServer`. The standard library describes it as a loopback HTTP server for end-to-end HTTP tests, so the production client, request construction, JSON decoding, cancellation, and timeout paths all run without external credentials ([`net/http/httptest`](https://pkg.go.dev/net/http/httptest)).

Assert method, path, query, authentication-header presence, content type, and body shape. Fixtures should cover pagination, empty pages, malformed responses, permission failures, `429`, transient `5xx`, and uncertain outcomes after a write may have reached Jira. Run shared behavioral suites against replaceable persistence and credential adapters where practical.

Local contracts are deterministic and safe enough for every change, but fixtures may drift from Jira Cloud.

### In-process Cobra tests

Construct a fresh root command per test, inject adapters and streams, set arguments with `SetArgs`, redirect input/output with `SetIn`, `SetOut`, and `SetErr`, and call `ExecuteContextC`. Cobra exposes these seams and explicitly documents `SetArgs` as useful for testing ([Cobra `Command` API](https://pkg.go.dev/github.com/spf13/cobra#Command)).

Assert command grammar, help, version output, stdout/stderr separation, returned errors, exit-status mapping, and calls made to injected adapters. Avoid package-global command and flag state, which can leak between tests.

This layer verifies CLI parsing and composition cheaply, but it does not prove real process behavior.

### Compiled-binary smoke tests

Launch the built executable for a deliberately small set of cases: `--help`, `--version`, invalid configuration, one no-state query, and one successful path through a loopback Jira server where possible. Assert exit status, stdout, and stderr.

This is the highest-fidelity and slowest layer. Cross-compilation proves only that a target builds, so release artifacts should also be executed on matching native runners.

## Jira Cloud test strategy

Use REST API v3 through the already-decided `https://api.atlassian.com/ex/jira/{cloudId}` route. Atlassian identifies v3 as the current Jira Cloud API and notes that most operations depend on the caller's permissions ([Jira Cloud REST API v3 introduction](https://developer.atlassian.com/cloud/jira/platform/rest/v3/intro/)).

For a personal CLI, email plus API token over Basic authentication is the simplest supported option; passwords must not be used. Atlassian reserves this approach for personal scripts, bots, and ad-hoc calls, while recommending OAuth 2.0 authorization-code grants for distributable integrations ([Jira REST API authentication guidance](https://developer.atlassian.com/cloud/jira/platform/rest/v3/intro/), [Basic authentication](https://developer.atlassian.com/cloud/jira/platform/basic-auth-for-rest-apis/)). OAuth avoids collecting users' API tokens but adds browser authorization, callback handling, refresh, and secure token storage.

The local contract suite should:

- follow the pagination fields returned by each operation instead of assuming a fixed page size;
- distinguish definite rejection from an uncertain result after a non-idempotent write;
- avoid blindly retrying Worklog creation after an uncertain outcome; and
- test bounded retries for safe reads.

Jira uses `429 Too Many Requests` for quota, burst, and per-issue write limits. `Retry-After` gives the wait in seconds, and some transient `503` responses may also include it. The client should honor it and use bounded backoff with jitter; inject its sleeper or clock so tests stay instant ([Jira Cloud rate limiting](https://developer.atlassian.com/cloud/jira/platform/rate-limiting/)).

Add an opt-in live test against a dedicated disposable Jira Cloud site and project. Atlassian provides Cloud developer sites specifically for development and testing ([developer-site setup](https://developer.atlassian.com/platform/forge/build-a-hello-world-app-in-jira/#set-up-a-cloud-developer-site)). The test should authenticate, query a known issue, create a uniquely marked Worklog, read it back, and clean it up. It should skip when secrets are absent, serialize mutations, never log credentials, and block releases rather than ordinary pull requests.

The live suite catches authentication, permissions, and API drift, at the cost of secrets, rate limits, latency, flakiness, and cleanup risk.

## Build and CI matrix

Build these four targets:

| `GOOS` | `GOARCH` | Artifact |
| --- | --- | --- |
| `linux` | `amd64` | `clock_<version>_linux_amd64.tar.gz` |
| `linux` | `arm64` | `clock_<version>_linux_arm64.tar.gz` |
| `darwin` | `amd64` | `clock_<version>_darwin_amd64.tar.gz` |
| `darwin` | `arm64` | `clock_<version>_darwin_arm64.tar.gz` |

Go lists all four as supported target combinations ([Go build environment](https://go.dev/doc/install/source#environment)). GitHub Actions supports job matrices, and its Go workflow guidance uses the standard `go build` and `go test` commands ([matrix jobs](https://docs.github.com/en/actions/how-tos/write-workflows/choose-what-workflows-do/run-job-variations), [building and testing Go](https://docs.github.com/en/actions/tutorials/build-and-test-code/go)).

Set `CGO_ENABLED=0` for release builds and choose dependencies with pure-Go implementations for all four targets. This removes a C toolchain and separately installed C-library dependency, but the executable may still call operating-system services such as Keychain or Secret Service at runtime. Describe the result as a **self-contained pure-Go executable**, not universally as “fully statically linked”: Go's own build tests skip external static linking on Darwin ([Go static-build test](https://go.dev/src/cmd/go/testdata/script/build_static.txt)).

Recommended gates:

- pull requests: formatting, vetting, unit/contract/Cobra tests, a race-detector job on Linux amd64, and four-target compilation;
- main: pull-request gates plus native compiled-binary smoke tests on Linux and macOS;
- release tag: main gates, the live Jira test, archive/checksum generation, and smoke execution of the exact extracted release artifacts before publication.

Native four-target execution costs more CI time than a single cross-build job, but catches platform build tags, executable startup, filesystem conventions, and native credential-adapter wiring.

## SemVer and version embedding

Use tags of the form `vMAJOR.MINOR.PATCH`. Go module versions require the `v` prefix and follow Semantic Versioning ([Go Modules Reference](https://go.dev/ref/mod#versions)). SemVer requires a declared public API, immutable published versions, patch increments for compatible fixes, minor increments for compatible additions, and major increments for incompatible changes ([Semantic Versioning 2.0.0](https://semver.org/#semantic-versioning-specification-semver)).

For this CLI, document the compatibility surface as:

- command, argument, and flag names and meanings;
- exit statuses and stdout/stderr behavior;
- machine-readable output schemas;
- configuration, state, and environment-variable contracts; and
- observable Jira mapping, retry, and uncertain-outcome behavior.

Use release candidates such as `v1.0.0-rc.1` for final validation, then publish `v1.0.0` when that surface and its acceptance tests are stable. Remaining on `v0.x` preserves freedom to break behavior, but SemVer explicitly signals that the public API is unstable.

Expose `clock --version` with the semantic version and source revision. Inject string values at link time with `-ldflags -X`; the Go linker officially supports `-X importpath.name=value` for suitable string variables ([Go linker options](https://pkg.go.dev/cmd/link#hdr-Command_Line)). Omit build time unless needed operationally, because timestamps prevent byte-for-byte reproducibility.

## Packaging and installation

Create a tag-triggered GitHub Release containing the four archives and `clock_<version>_checksums.txt`. Each archive should include the executable, license, and brief installation text. GitHub Releases are tag-based deployable iterations and support attached binary assets ([About releases](https://docs.github.com/en/repositories/releasing-projects-and-managing-releases/about-releases)). When immutable releases are enabled, GitHub recommends drafting the release, attaching every asset, and publishing only afterward ([Managing releases](https://docs.github.com/en/repositories/releasing-projects-and-managing-releases/managing-releases-in-a-repository)).

The baseline installation flow is:

1. download the archive matching the operating system and architecture;
2. verify its SHA-256 checksum;
3. extract `clock`; and
4. place it in a user-owned directory on `PATH`, such as `~/.local/bin`, or an administrator-managed directory such as `/usr/local/bin`.

This path requires no Go runtime. `go install github.com/JirakLu/clock@vX.Y.Z` can be a secondary developer path, but it requires a Go toolchain and does not install the exact checksummed release asset. Modern Go assigns versioned executable installation to `go install`, not `go get` ([Go executable-install guidance](https://go.dev/doc/go-get-install-deprecation)).

For macOS, v1 ships an unsigned archive because broader multi-user distribution is outside this map's destination. The release notes must disclose the limitation rather than silently instructing users to bypass Gatekeeper. Apple says external build systems should add a manual Developer ID signing step, and its notary service supports scripted command-line workflows ([distribution signing](https://developer.apple.com/documentation/xcode/creating-distribution-signed-code-for-the-mac), [notarization](https://developer.apple.com/documentation/security/notarizing-macos-software-before-distribution)). Signing and notarization require Apple credentials, certificates, and release-pipeline work; revisit them if the distribution scope expands.

Homebrew improves discovery, upgrades, and uninstallation. A formula can install platform-specific release assets and verify their checksums, following Homebrew's official formula conventions ([Homebrew Formula Cookbook](https://docs.brew.sh/Formula-Cookbook)). The tradeoff is another repository or tap, platform metadata, release credentials, and an additional failure surface. Add it after direct archives and the macOS signing policy are stable; direct GitHub Release installation remains the required channel.

## Decision summary

- Prefer deterministic local tests, with a small release-only live Jira suite.
- Test command composition in process, then reserve subprocess tests for process and artifact guarantees.
- Build all four OS/architecture combinations with `CGO_ENABLED=0`, and execute exact release artifacts natively.
- Declare the CLI compatibility surface before `v1.0.0`; embed version and revision, but not build time.
- Publish GitHub Release archives and checksums first. The macOS v1 archive is explicitly unsigned; add signing, notarization, or Homebrew when broader distribution justifies their maintenance and trust costs.
