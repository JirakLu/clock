# Implementation language and CLI ecosystem

Research date: 2026-07-26

## Decision

Use **Go with Cobra** for v1.

Keep the core independent of Cobra behind small interfaces (Jira client, timer store,
credential store, clock, and output renderer), and use the standard library for HTTP,
JSON, filesystem paths, and tests. Use an OS-keyring adapter for the Jira API token.

This is not a claim that Go is universally better than Rust or TypeScript. It is the
lowest-complexity fit for this particular program: a small personal HTTP client whose
hard requirements are native Linux/macOS delivery, predictable CLI behavior, quick
tests, and room to add commands.

## Criteria

The ticket asks for:

- a self-contained executable for Linux and macOS;
- strong command ergonomics;
- secure configuration options;
- JSON output and a capable HTTP client;
- fast, straightforward tests;
- simple packaging; and
- an extensible structure without implementing speculative features.

## Comparison

| Choice | Native delivery | CLI ergonomics | HTTP, JSON, tests | Secrets/config | Cost and risk |
| --- | --- | --- | --- | --- | --- |
| **Go + Cobra** | `go build` produces an executable. The official toolchain targets both `darwin` and `linux`, on `amd64` and `arm64`; target selection is controlled by `GOOS`/`GOARCH`.[^go-build][^go-targets] | Cobra provides nested subcommands, POSIX-style flags, generated help, suggestions, aliases, and shell completions.[^cobra] | `net/http`, `encoding/json`, `testing`, and `net/http/httptest` are standard-library packages; successful `go test` results are cached.[^go-http][^go-json][^go-test][^go-httptest] | `os.UserConfigDir` follows XDG on Linux and Application Support on macOS. `go-keyring` exposes macOS Keychain and Linux Secret Service behind one API.[^go-config][^go-keyring] | Small toolchain and dependency surface; uncomplicated release matrix. Cobra is runtime-wired rather than type-derived, so command constructors need focused tests. |
| **Rust + clap** | Rust emits native executables and `rustc` is a cross-compiler, but non-host builds also need the target standard library and, where required, a suitable linker.[^rust-targets] | clap can derive typed arguments and subcommands from structs/enums, giving especially strong compile-time command modeling.[^clap] | `reqwest` supplies HTTPS clients, Serde handles typed serialization, and `cargo test` runs unit, integration, and documentation tests.[^reqwest][^serde][^cargo-test] | The `dirs` crate provides platform config paths; the keyring ecosystem supports platform credential stores.[^rust-dirs][^rust-keyring] | Excellent correctness and long-term structure, but ownership/async choices, longer builds, and cross-linking add complexity that this I/O-bound personal CLI does not need. |
| **TypeScript + Bun** | `bun build --compile` embeds the application, packages, and Bun runtime into one executable and can target Linux and macOS on x64/arm64.[^bun-compile] | Mature npm command parsers are available, and TypeScript makes command/application modeling approachable. | Bun provides web `fetch`, JSON, and a fast Jest-compatible test runner with TypeScript support.[^bun-test] | Platform keyring packages exist, but secure-store behavior becomes an npm/native-integration choice rather than a built-in Bun facility. | Fastest iteration for a TypeScript-oriented developer, but the output embeds a runtime. Bun also states that its bundler does not type-check, and its Jest compatibility is incomplete, requiring a separate type-check step and acceptance of a younger packaging stack.[^bun-bundler][^bun-test] |

## Why Go wins here

1. **Packaging is a normal build operation.** The required OS/architecture matrix is
   directly supported by the Go toolchain. With a pure-Go application layer, release
   artifacts can be built without asking users to install Go. The keyring adapter is
   the only platform-sensitive edge and should remain behind an interface.
2. **The standard library covers the center of the product.** Jira is HTTPS plus JSON,
   and Go includes the client, codec, filesystem, time, and test-server primitives
   needed for that work. This avoids selecting an application framework.
3. **Cobra matches future command growth.** Generated help/completions, nested commands,
   aliases, and testable argument injection cover both the proposed v1 and later
   additions without dictating the domain architecture.
4. **Testing the important seams is cheap.** The Jira adapter can run against
   `httptest.Server`; time-dependent behavior can use an injected clock; commands can
   receive explicit arguments and in-memory streams. No live Jira account or process
   spawning is needed for the core suite.
5. **Rust's extra guarantees do not buy much for this workload.** Rust plus clap would
   be the runner-up and is a sound choice if the maintainer strongly prefers Rust.
   Here, however, most failures are remote API, validation, time-zone, and state
   semantics—not memory-management failures—so its additional build and learning
   complexity has little compensating value.
6. **Bun's convenience is less valuable than release predictability.** It is viable,
   but bundling an entire runtime and adding a separate type-check stage are avoidable
   for a compact native CLI.

## Recommended ecosystem boundary

Adopt only these choices at the language level:

- **Go** as the implementation language.
- **Cobra** for parsing, help, completions, and command dispatch.
- Go standard library `net/http` and `encoding/json` for Jira transport.
- Go standard library `testing` and `httptest` for unit and adapter tests.
- `os.UserConfigDir` for non-secret configuration and timer-state location.
- A narrow credential-store interface with a Linux Secret Service/macOS Keychain
  implementation such as `zalando/go-keyring`.

Do **not** select a broad configuration framework, dependency-injection framework,
ORM, or local database as part of this decision. Those are neither required by the
language nor justified by v1.

## Consequences for later tickets

- Packaging should specify four artifacts unless supported CPU scope is narrowed:
  `linux/amd64`, `linux/arm64`, `darwin/amd64`, and `darwin/arm64`.
- The configuration/authentication decision must define what happens on Linux systems
  with no Secret Service provider. The keyring library documents that dependency, so
  this cannot be hidden as a language concern.[^go-keyring]
- Architecture should keep Cobra and keyring types out of the domain/application
  layer, preserving the option to replace either dependency.
- JSON output should serialize explicit output DTOs, not Cobra or Jira transport
  structs, so the machine-readable contract can evolve intentionally.

## Primary sources

[^go-build]: Go documentation, [Compile and install the application](https://go.dev/doc/tutorial/compile-install).
[^go-targets]: Go documentation, [Installing Go from source: supported operating systems and `GOOS`/`GOARCH`](https://go.dev/doc/install/source#environment).
[^cobra]: Cobra source repository, [Overview and features](https://github.com/spf13/cobra#overview).
[^go-http]: Go standard library, [`net/http`](https://pkg.go.dev/net/http).
[^go-json]: Go standard library, [`encoding/json`](https://pkg.go.dev/encoding/json).
[^go-test]: Go command source documentation, [`go test`](https://pkg.go.dev/cmd/go/internal/test).
[^go-httptest]: Go standard library, [`net/http/httptest`](https://pkg.go.dev/net/http/httptest).
[^go-config]: Go standard library, [`os.UserConfigDir`](https://pkg.go.dev/os#UserConfigDir).
[^go-keyring]: `zalando/go-keyring` source repository, [platform dependencies and usage](https://github.com/zalando/go-keyring#dependencies).
[^rust-targets]: Rust compiler documentation, [Targets](https://doc.rust-lang.org/rustc/targets/index.html).
[^clap]: clap documentation, [derive API](https://docs.rs/clap/latest/clap/_derive/).
[^reqwest]: reqwest documentation, [`Client`](https://docs.rs/reqwest/latest/reqwest/struct.Client.html).
[^serde]: Serde project documentation, [Using derive](https://serde.rs/derive.html).
[^cargo-test]: Cargo documentation, [`cargo test`](https://doc.rust-lang.org/cargo/commands/cargo-test.html).
[^rust-dirs]: dirs crate documentation, [platform-specific directories](https://docs.rs/dirs/latest/dirs/).
[^rust-keyring]: Rust keyring documentation, [platform-independent secret access](https://docs.rs/keyring/latest/keyring/).
[^bun-compile]: Bun documentation, [Single-file executables and cross-compilation targets](https://bun.sh/docs/bundler/executables).
[^bun-test]: Bun documentation, [Test runner](https://bun.sh/docs/test).
[^bun-bundler]: Bun documentation, [Bundler](https://bun.sh/docs/bundler).
