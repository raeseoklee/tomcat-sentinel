# jvm-sentinel

[![CI](https://github.com/raeseoklee/jvm-sentinel/actions/workflows/ci.yml/badge.svg)](https://github.com/raeseoklee/jvm-sentinel/actions/workflows/ci.yml)

Lightweight JVM service sentinel for Linux MVP.

The sentinel monitors one JVM service process, detects abnormal shutdown patterns such as JVM OOM, backs up relevant logs, and restarts the service through `catalina.sh` or a custom command.

The repository name is `jvm-sentinel` rather than `tomcat-sentinel` because the recovery engine is PID/log/command based and supports Tomcat, Netty, and other single JVM services.

## MVP Scope

- Linux only
- `x86_64` and `aarch64` binary targets
- Single Tomcat instance per sentinel process
- Single Netty or generic JVM process per sentinel process through custom commands
- PID-based liveness check
- Log pattern check for abnormal shutdown evidence
- Log backup before restart
- Restart through `catalina.sh` by default
- Custom start/stop/status command support
- Service startup through `systemd`
- Dependency-light implementation target
- Safe to run beside JVM services on very small servers, including 1 vCPU / 512 MiB RAM hosts

Non-MVP targets:

- Multiple Tomcat instances in one process
- Windows service support
- macOS launchd support
- Commercial monitoring system integration
- Remote command execution
- Web UI

## Implementation Direction

Use Go for the first implementation.

Reasons:

- Small static binaries for Linux `amd64` and `arm64`
- Low idle memory footprint
- Strong process, signal, timeout, and filesystem support in the standard library
- Straightforward systemd deployment
- Good future portability to macOS and Windows

Rust can produce a smaller sentinel, but Go is the better MVP tradeoff for this project because cross-platform packaging, service integration, and long-term maintenance are simpler while still meeting the 1 vCPU / 512 MiB resource budget.

The MVP should avoid YAML/TOML dependencies and start with a simple `key=value` properties file plus environment variable overrides. A richer config format can be added later if the operational need is clear.

## Low-Memory Requirement

The sentinel must be designed as an emergency recovery process, not another meaningful memory consumer.

MVP default target hardware profile:

- 1 vCPU
- 512 MiB RAM
- Tomcat and sentinel running on the same host

MVP resource targets for the sentinel:

- Idle RSS target: 12 MiB or lower
- Normal scan/recovery RSS target: 24 MiB or lower, excluding short-lived shell command overhead
- Idle CPU target: near zero between checks
- No full-log reads
- No unbounded stdout/stderr capture
- No background worker pools
- No embedded HTTP server in the MVP
- Streaming log backup with a small fixed buffer
- No compression by default, because compression competes with Tomcat on 1 vCPU hosts

For 1 vCPU / 512 MiB servers, Tomcat heap sizing still has to be controlled separately. The sentinel can restart Tomcat after OOM, but it cannot make an oversized JVM stable by itself.

The same applies to Netty or other JVM applications: the sentinel can restore a failed process, but JVM heap, metaspace, direct memory, and thread stack sizing must fit the host.

## Netty And Generic JVM Apps

Tomcat is the default profile, but the monitor/recovery engine is generic. For Netty, set:

- `app.kind=netty`
- `app.home`
- `app.base`
- `pid.file`
- `log.paths`
- `start.command`
- `stop.command`
- `process.command_hint`

See [config/netty.properties.example](config/netty.properties.example).

## Configuration

Tomcat:

```sh
cp config/tomcat.properties.example /etc/jvm-sentinel/tomcat.properties
```

Netty or another JVM service:

```sh
cp config/netty.properties.example /etc/jvm-sentinel/netty.properties
```

Environment overrides use the `JVM_SENTINEL_` prefix, for example `JVM_SENTINEL_CHECK_INTERVAL=30s`. The older `TOMCAT_SENTINEL_` prefix is accepted for compatibility.

## Initial Artifacts

- [Architecture](docs/architecture.md)
- [Implementation language decision](docs/decisions/0001-use-go-for-mvp.md)
- [Tomcat config example](config/tomcat.properties.example)
- [Netty config example](config/netty.properties.example)
- [Tomcat systemd service template](packaging/systemd/jvm-sentinel-tomcat.service)
- [Netty systemd service template](packaging/systemd/jvm-sentinel-netty.service)

## Build

```sh
go test ./...
make build
make dist-linux VERSION=0.1.0
```

Linux outputs:

- `dist/jvm-sentinel-linux-amd64`
- `dist/jvm-sentinel-linux-arm64`

## Run

One-shot check:

```sh
jvm-sentinel -config /etc/jvm-sentinel/tomcat.properties -once
```

Long-running service mode:

```sh
jvm-sentinel -config /etc/jvm-sentinel/tomcat.properties
```

The default production path is systemd using [packaging/systemd/jvm-sentinel-tomcat.service](packaging/systemd/jvm-sentinel-tomcat.service) or [packaging/systemd/jvm-sentinel-netty.service](packaging/systemd/jvm-sentinel-netty.service).

## Live Smoke

Docker-based Linux live smoke:

```sh
scripts/docker-live-smoke.sh
```

The smoke test runs a fake Netty/JVM process inside Linux, verifies `/proc` PID checks, classifies an OOM log, backs up logs, runs the configured start command, and confirms the restarted process is alive.
