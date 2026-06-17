# Tomcat Sentinel Architecture

## 1. Goal

Build a lightweight watchdog process that keeps one Tomcat or single JVM service process available when the JVM or process exits abnormally, especially after OOM-related shutdowns.

The sentinel must:

- Monitor a configured PID source.
- Detect process death.
- Inspect recent application logs for abnormal shutdown evidence.
- Back up configured logs before restart.
- Restart Tomcat with `catalina.sh start` by default, or another JVM service with a custom command.
- Allow custom commands for start, stop, status, and PID resolution.
- Support Netty and other single JVM services through generic `app.*` settings.
- Run as a service on Linux.
- Run safely beside JVM services on very small hosts, including 1 vCPU / 512 MiB RAM servers.

## 2. MVP Runtime Model

The MVP is a single long-running Go process.

```text
+-------------------+
| tomcat-sentinel      |
+---------+---------+
          |
          v
+-------------------+       +--------------------+
| Monitor Loop      +------>+ PID Resolver       |
+---------+---------+       +--------------------+
          |
          v
+-------------------+       +--------------------+
| Failure Detector  +------>+ Log Scanner        |
+---------+---------+       +--------------------+
          |
          v
+-------------------+       +--------------------+
| Recovery Manager  +------>+ Log Backup Manager |
+---------+---------+       +--------------------+
          |
          v
+-------------------+       +--------------------+
| Command Runner    +------>+ catalina.sh/custom |
+-------------------+       +--------------------+
```

The process should not embed a scheduler, agent framework, HTTP server, or plugin system in the MVP.

The implementation should use Go's standard library first. New third-party dependencies require a concrete operational reason because every dependency affects binary size, cross-platform packaging, and the low-memory budget.

## 3. Low-Memory Design Budget

The sentinel is a recovery guard. It must stay small enough that it does not materially compete with the watched JVM service on constrained hosts.

Default tiny profile:

- CPU: 1 vCPU
- Memory: 512 MiB RAM
- Deployment: JVM service and sentinel on the same host
- Priority: minimize steady-state resource use over sub-second detection latency

Target budget for the MVP:

- Idle RSS: <= 12 MiB
- Normal scan/recovery RSS: <= 24 MiB, excluding short-lived `/bin/sh`, `catalina.sh`, and JVM startup overhead
- Idle CPU: near zero between checks
- Check interval: default 15 seconds for the tiny profile
- Goroutines: one main monitor loop plus short-lived command/log tasks only
- File buffers: fixed-size buffers, default 32 KiB for copies
- Log scan window: bounded tail reads, default 512 KiB per file
- Log files scanned: default maximum 4 files per detection cycle
- Command output capture: bounded, default 32 KiB per command

Implementation constraints:

- Use Go standard library only for the MVP.
- Build as one static or mostly-static CLI binary named `tomcat-sentinel`.
- Keep the monitor loop synchronous unless a specific command requires a short-lived goroutine for timeout handling.
- Parse `key=value` config directly; do not add YAML/TOML libraries initially.
- Match log patterns with substring checks, not regular expressions, unless a later version proves regex is needed.
- Inspect `/proc/<pid>` directly on Linux instead of spawning `ps` for every check.
- Compile and store pattern lists once at startup.
- Never read an entire application log into memory.
- Copy backups with streaming IO.
- Bound the number of log files scanned per cycle.
- Do not keep historical incidents in memory; write manifests to disk.
- Do not compress incident backups by default on 1 vCPU hosts.
- Avoid polling faster than the configured check interval.

Recommended low-memory runtime settings:

- Set `GOMEMLIMIT=24MiB` for the sentinel process.
- Set `GOGC=50` to bias Go toward lower memory use.
- Avoid systemd `MemoryMax` in the default unit when sentinel starts Tomcat with `catalina.sh`, because Tomcat child processes may remain in the same service cgroup.

The sentinel should log a warning when its own memory exceeds the configured soft limit, but MVP recovery behavior should not depend on self-memory sampling.

## 4. Core Components

### 4.1 Config Loader

Reads one properties file and applies environment overrides.

Required fields:

- `app.home`
- `app.base`
- `pid.file` or `pid.command`
- `log.paths`
- `backup.dir`

Important optional fields:

- `start.command`
- `stop.command`
- `status.command`
- `check.interval`
- `restart.cooldown`
- `restart.max_attempts`
- `restart.window`
- `command.timeout`
- `oom.patterns`
- `shutdown.patterns`
- `backup.retention.days`
- `log.scan_tail_bytes`
- `log.scan_max_files`
- `command.output_max_bytes`
- `resource.soft_rss_limit_mb`

Environment variables should use the `TOMCAT_SENTINEL_` prefix. For example, `TOMCAT_SENTINEL_CONFIG`, `TOMCAT_SENTINEL_START_COMMAND`, and `TOMCAT_SENTINEL_CHECK_INTERVAL`.

The legacy `JVM_SENTINEL_` prefix is accepted for compatibility, but new deployments should use `TOMCAT_SENTINEL_`.

Compatibility aliases:

- `tomcat.home` maps to `app.home`
- `tomcat.base` maps to `app.base`

Tomcat defaults remain in the default config. Netty and generic JVM services should set `app.kind` and override commands, PID file, logs, and process command hints.

### 4.2 PID Resolver

PID resolution order:

1. `pid.command`, when configured
2. `pid.file`, when configured
3. `status.command`, when configured

The MVP should prefer `pid.file`. Tomcat supports `CATALINA_PID`; Netty or generic JVM apps should create a PID file in their wrapper script.

Liveness check:

- Linux: check `/proc/<pid>` exists and the process is not a zombie.
- Optional command verification: run `status.command` if configured.

The sentinel must avoid acting on unrelated processes. If a PID exists but its command line does not match a configured process hint, recovery should stop and log a high-severity error.

### 4.3 Log Scanner

The scanner should inspect only recent log tails, not entire log files.

Default files:

- `${tomcat.base}/logs/catalina.out`
- `${tomcat.base}/logs/catalina.*.log`
- `${tomcat.base}/logs/localhost.*.log`

Default OOM patterns:

- `java.lang.OutOfMemoryError`
- `GC overhead limit exceeded`
- `Java heap space`
- `Metaspace`
- `unable to create native thread`
- `Killed`
- `Out of memory: Kill process`

Default shutdown patterns:

- `Destroying ProtocolHandler`
- `Stopping service [Catalina]`
- `Server startup in`
- `SEVERE`

The scanner produces evidence, not the final decision. Process death remains the primary trigger; log evidence classifies the incident.

Netty apps should point `log.paths` at their application logs, such as `${app.base}/logs/app.log` and rotated variants.

Low-memory scanning rules:

- Open one file at a time.
- Seek to `max(0, size - log.scan_tail_bytes)`.
- Read in small chunks.
- Stop after `log.scan_max_files` matched files.
- Store only matched evidence lines or short excerpts.
- Do not rescan logs while the watched process is healthy; scan only after a confirmed down event.

### 4.4 Log Backup Manager

Before restart, copy configured log files into an incident directory.

Directory format:

```text
<backup.dir>/
  20260617T103055Z-oom-pid-12345/
    manifest.json
    catalina.out
    catalina.2026-06-17.log
```

`manifest.json` should include:

- incident time
- previous PID
- detection reason
- matched patterns
- source files
- copied byte counts
- command exit codes
- sentinel version

Backup rules:

- Copy only files matching `log.paths`.
- Support per-file max bytes by copying the tail when files are large.
- Never delete source logs during backup.
- Apply retention after a successful recovery attempt.
- Copy with `backup.copy_buffer_bytes`, default 32 KiB.
- Default `backup.max_bytes_per_file` should stay small for constrained hosts.
- Default backups are plain copied tails, not compressed archives.

### 4.5 Recovery Manager

Recovery state machine:

```text
RUNNING
  |
  | process missing
  v
SUSPECT_DOWN
  |
  | confirm after debounce
  v
CLASSIFY_INCIDENT
  |
  v
BACKUP_LOGS
  |
  v
RESTARTING
  |
  +-- success --> VERIFY_RUNNING --> RUNNING
  |
  +-- failure --> COOLDOWN --> RETRY or GIVE_UP
```

Rules:

- Confirm process death with a short debounce to avoid reacting to transient PID file updates.
- Run backup before start command.
- Enforce restart cooldown.
- Enforce max attempts inside a time window.
- Exit non-zero or stay degraded after repeated restart failure, based on config.
- Optionally delay restart when `/proc/meminfo` reports `MemAvailable` below `restart.min_mem_available_mb`.

### 4.6 Command Runner

Default commands:

```sh
${tomcat.home}/bin/catalina.sh start
${tomcat.home}/bin/catalina.sh stop
```

Generic JVM services should override these with wrapper scripts:

```sh
${app.home}/bin/start.sh
${app.home}/bin/stop.sh
```

The runner must:

- Execute commands with a fixed timeout.
- Set configured environment variables.
- Set working directory to `app.home` unless overridden.
- Capture stdout and stderr into sentinel logs.
- Return structured exit status.
- Capture only `command.output_max_bytes` per command to prevent noisy scripts from increasing memory use.

Custom commands should be strings executed through `/bin/sh -c` in the MVP. Later versions can add array-style commands to avoid shell parsing ambiguity.

The runner always provides `APP_NAME`, `APP_KIND`, `APP_HOME`, and `APP_BASE`. It provides `CATALINA_HOME`, `CATALINA_BASE`, and `CATALINA_PID` only when `app.kind=tomcat`.

## 5. Sentinel Logs

The sentinel should write its own log separately from application logs.

Default paths:

- stdout/stderr under systemd journal
- optional file: `/var/log/tomcat-sentinel/sentinel.log`

Minimum event types:

- startup config summary
- PID check result changes
- abnormal shutdown detection
- log backup result
- command execution result
- restart success/failure
- rate-limit/give-up events

Log format:

- MVP: human-readable line logs
- Later: optional JSON lines

## 6. Linux Service Design

Run with systemd as a normal service:

- `Restart=always` is for the sentinel process, not the watched JVM service.
- JVM service restart policy stays inside sentinel logic.
- Use a dedicated Unix user when possible, such as `tomcat` for Tomcat or `netty` for a Netty service.
- Grant read access to application logs and execute access to the configured start/stop scripts.
- Avoid root unless the watched service itself requires root-owned operations.
- Prefer Go runtime memory knobs over systemd memory caps when the JVM service is launched by the sentinel process.

Recommended service behavior:

- `ExecStart=/usr/local/bin/tomcat-sentinel -config /etc/tomcat-sentinel/tomcat.properties`
- `Restart=always`
- `RestartSec=5`
- `KillSignal=SIGTERM`
- `Environment=GOMEMLIMIT=24MiB`
- `Environment=GOGC=50`

Do not add `MemoryMax=...` to the default systemd unit while `catalina.sh start` is used directly. Java child processes can remain inside the same service cgroup, so a sentinel memory cap can accidentally cap Tomcat too. A hard `MemoryMax` is only safe when Tomcat is started into a separate service or cgroup.

Do not add `Nice=...` or `CPUQuota=...` to the default unit for the same reason: Tomcat may inherit the service-level scheduling policy when it is launched by `catalina.sh`.

## 7. Failure Decision Policy

MVP trigger:

1. Resolve PID.
2. If no PID or PID is not alive, wait debounce.
3. Resolve again.
4. If still down, scan logs.
5. Back up logs.
6. Start the configured JVM service.
7. Verify new PID is alive.

Incident classification:

- `oom`: process down and OOM pattern matched
- `shutdown`: process down and shutdown pattern matched
- `crash`: process down with no useful pattern
- `unknown`: PID cannot be trusted

The sentinel should restart for `oom`, `shutdown`, and `crash` by default. It should not restart for `unknown` unless `restart.on_unknown=true`.

## 8. Safety Constraints

- Do not run `kill -9` in the MVP.
- Do not remove or truncate application logs.
- Do not overwrite backups.
- Do not restart if PID belongs to an unexpected process.
- Do not retry forever without cooldown and attempt limits.
- Do not silently ignore backup failure; make restart-on-backup-failure configurable.
- Do not allocate memory proportional to log file size.
- Do not add systemd memory limits that also constrain JVM service child processes.

## 9. Cross-Platform Future

Keep OS-specific behavior behind narrow interfaces:

- `ProcessInspector`
- `ServiceAdapter`
- `CommandRunner`
- `PathResolver`

Future adapters:

- Linux: `/proc`, systemd
- macOS: `launchd`, `ps`
- Unix: `ps`, init scripts, rc.d
- Windows: Windows Service API, Event Log, PowerShell commands

The MVP should not overbuild these abstractions, but file and package boundaries should leave room for them.

## 10. Suggested Source Layout

```text
cmd/tomcat-sentinel/main.go
internal/config/
internal/process/
internal/logscan/
internal/backup/
internal/recovery/
internal/command/
internal/sentinel/
packaging/systemd/
config/
docs/
```

## 11. MVP Acceptance Criteria

- Starts with a config file.
- Detects a missing JVM service PID.
- Classifies OOM from recent logs.
- Backs up configured logs into an incident directory.
- Runs default or custom start command.
- Verifies the JVM service is alive after restart.
- Rate-limits repeated restart attempts.
- Runs under systemd.
- Builds Linux `amd64` and `arm64` binaries.
- Keeps idle RSS under the low-memory budget on Linux.
- Scans and backs up logs without loading whole files into memory.
- On the tiny profile, performs healthy-state checks without measurable sustained CPU load.
