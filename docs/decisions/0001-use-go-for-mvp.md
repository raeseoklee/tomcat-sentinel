# Use Go for the MVP

## Status

Accepted.

## Context

JVM Sentinel must run beside Tomcat, Netty, or another single JVM service on very small servers, including 1 vCPU / 512 MiB RAM hosts. It also needs a future path to Linux, Unix-like systems, macOS, and Windows.

The main implementation choices considered were Go and Rust.

Rust can produce a smaller resident process and has no garbage collector. That is attractive for an always-on sentinel. The tradeoff is higher release engineering complexity across Linux `amd64`, Linux `arm64`, macOS, Windows, and future Unix-like targets, especially around target toolchains, linker behavior, libc compatibility, native dependencies, and packaging.

Go has a larger runtime footprint than Rust, but the expected sentinel workload is small: PID file checks, direct Linux `/proc` inspection, bounded log tail scanning, streaming log backup, and command execution. With strict implementation constraints, Go can stay within the resource target while reducing build, packaging, service integration, and maintenance complexity.

## Decision

Implement the MVP in Go.

The MVP must keep the Go implementation small:

- Standard library first.
- No embedded HTTP server.
- No background worker pool.
- No full-log reads.
- No YAML/TOML dependency in the MVP.
- No regex-based log scanner unless substring matching proves insufficient.
- Bounded command output capture.
- Bounded log scan window.
- Streaming backup with fixed-size buffers.
- Linux PID checks through direct `/proc` inspection instead of recurring external commands.

Default tiny profile target:

- Host: 1 vCPU / 512 MiB RAM
- Idle RSS: <= 12 MiB
- Normal scan/recovery RSS: <= 24 MiB, excluding short-lived shell and JVM service startup overhead
- Healthy-state CPU: near zero between checks

## Consequences

Positive:

- Simple Linux `amd64` and `arm64` builds.
- Easier future Windows and macOS support.
- Straightforward systemd packaging.
- Good enough memory profile for the target host class when implementation constraints are followed.
- Lower contributor and operator burden than Rust for this project.

Negative:

- Runtime RSS will be higher than a comparable Rust implementation.
- Go GC behavior must be controlled with bounded allocations and runtime settings.
- CI should include a resource smoke test so memory growth is caught early.

## Rejected

- Rust: lower memory footprint, but higher cross-platform release engineering burden for the MVP.
- C: smallest runtime footprint, but higher memory-safety and maintenance risk.
- Shell: simple initial prototype, but repeated external processes, weak error handling, and fragile cross-platform behavior make it a poor fit for a reliable sentinel.
- Python/Node/Java: unnecessary runtime overhead for the target 1 vCPU / 512 MiB deployment.

## Verification

The first implementation should include a resource smoke test that runs the healthy monitor loop against a fake JVM service PID file and verifies:

- No full-log reads occur.
- RSS stays under the configured tiny-profile soft limit.
- Healthy-state checks do not spawn external commands when `pid.file` is configured.
