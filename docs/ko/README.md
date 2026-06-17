# tomcat-sentinel 한국어 가이드

`tomcat-sentinel`은 Tomcat 프로세스를 감시하다가 OOM, 비정상 종료, crash 등으로 프로세스가 내려간 경우 로그를 확인하고, 로그를 백업한 뒤 Tomcat을 자동 재기동하는 경량 sentinel 프로세스입니다.

기본 대상은 Tomcat입니다. 다만 내부 복구 엔진은 PID, 로그, 명령어 기반이라 Netty나 단일 JVM 서비스도 custom command로 감시할 수 있습니다.

## 목표

- Tomcat PID 생존 여부 확인
- OOM, shutdown, crash 로그 패턴 확인
- 재기동 전 로그 백업
- `catalina.sh start` 기본 재기동
- custom start/stop/status 명령 지원
- systemd 서비스 형태 실행
- 1 vCPU / 512 MiB 같은 소형 Linux 서버에서도 Tomcat 옆에서 동작
- Linux `amd64`, `arm64` 빌드 지원

## 비목표

- 하나의 sentinel 프로세스에서 여러 Tomcat 인스턴스 관리
- Windows 서비스 지원
- macOS launchd 지원
- 웹 UI
- 원격 서버 제어
- 외부 모니터링 시스템 연동

이 항목들은 MVP 이후 확장 대상으로 둡니다.

## 동작 방식

정상 상태에서는 가볍게 PID만 확인합니다.

1. `pid.file` 또는 `pid.command`로 PID를 찾습니다.
2. Linux에서는 `/proc/<pid>`로 프로세스 생존 여부를 확인합니다.
3. 프로세스가 없으면 짧은 debounce 후 한 번 더 확인합니다.
4. 여전히 내려간 상태면 최근 로그 tail만 읽어 OOM/shutdown/crash를 분류합니다.
5. 설정된 로그를 incident 디렉터리에 백업합니다.
6. `start.command`를 실행합니다.
7. 새 PID가 살아있는지 확인합니다.

정상 상태에서는 로그 파일을 반복 스캔하지 않습니다.

단, sentinel이 실행 중인 상태에서 감시 대상 PID가 바뀌면 외부 재시작으로 보고 로그 백업만 수행할 수 있습니다. 이 기능은 `tomcat.service`의 `Restart=on-failure`처럼 systemd가 Tomcat을 먼저 살려버리는 구성을 보완하기 위한 것입니다.

## 저사양 서버 기준

기본 설정은 1 vCPU / 512 MiB 서버를 기준으로 보수적으로 잡혀 있습니다.

- 체크 주기: `15s`
- 로그 스캔: 파일당 최근 `512 KiB`
- 스캔 파일 수: 최대 `4개`
- 명령 출력 캡처: 최대 `32 KiB`
- 백업 파일당 최대 크기: `8 MiB`
- sentinel RSS 목표: idle `12 MiB` 이하, 복구 작업 중 `24 MiB` 이하

Tomcat의 JVM 메모리 설정은 별도로 맞춰야 합니다. sentinel은 OOM 이후 재기동은 할 수 있지만, JVM heap/metaspace/thread stack 설정이 서버 용량보다 크면 안정화할 수 없습니다.

## 빌드

```sh
go test ./...
make build
make dist-linux VERSION=0.1.0
```

Linux 산출물:

- `dist/tomcat-sentinel-linux-amd64`
- `dist/tomcat-sentinel-linux-arm64`

## Tomcat 설정 예시

예시 파일을 복사합니다.

```sh
sudo mkdir -p /etc/tomcat-sentinel
sudo cp config/tomcat-sentinel.properties.example /etc/tomcat-sentinel/tomcat.properties
```

핵심 설정:

```properties
app.name=tomcat
app.kind=tomcat
app.home=/opt/tomcat
app.base=/opt/tomcat

pid.file=/opt/tomcat/temp/tomcat.pid
log.paths=/opt/tomcat/logs/catalina.out,/opt/tomcat/logs/catalina.*.log,/opt/tomcat/logs/localhost.*.log
backup.paths=/opt/tomcat/logs/catalina.out
backup.dir=/var/backups/tomcat-sentinel

start.command=/opt/tomcat/bin/catalina.sh start
stop.command=/opt/tomcat/bin/catalina.sh stop
process.command_hint=org.apache.catalina.startup.Bootstrap
```

Tomcat은 `CATALINA_PID`를 반드시 안정적으로 쓰도록 맞추는 것이 좋습니다.

`log.paths`는 장애 시점에 스캔할 로그이고, `backup.paths`는 실제 incident 디렉터리에 복사할 로그입니다. `backup.paths`를 비워두면 `log.paths` 전체가 백업됩니다. 작은 디스크에서는 rotated 로그 전체가 매번 복사되지 않도록 `backup.paths`를 `catalina.out`처럼 좁게 시작하는 편이 안전합니다.

## Netty 설정 예시

Netty나 다른 JVM 서비스는 wrapper script가 PID 파일을 만들어야 합니다.

```sh
sudo cp config/netty-sentinel.properties.example /etc/tomcat-sentinel/netty.properties
```

핵심 설정:

```properties
app.name=netty-api
app.kind=netty
app.home=/opt/netty-api
app.base=/var/lib/netty-api

pid.file=${app.base}/run/netty-api.pid
log.paths=${app.base}/logs/app.log,${app.base}/logs/app.*.log
backup.paths=${app.base}/logs/app.log
start.command=${app.home}/bin/start.sh
stop.command=${app.home}/bin/stop.sh
process.command_hint=com.example.netty.Main
```

## systemd 실행

Tomcat용 템플릿:

```sh
sudo cp packaging/systemd/tomcat-sentinel.service /etc/systemd/system/tomcat-sentinel.service
sudo systemctl daemon-reload
sudo systemctl enable --now tomcat-sentinel
```

상태 확인:

```sh
systemctl status tomcat-sentinel
journalctl -u tomcat-sentinel -f
```

Netty용 템플릿은 `packaging/systemd/tomcat-sentinel-netty.service`를 참고합니다.

### Tomcat이 별도 systemd 서비스일 때

이미 `tomcat.service`가 Tomcat을 관리하고 있다면 sentinel이 `catalina.sh`를 직접 실행하지 않고 systemd에 위임하는 편이 충돌이 적습니다.

```properties
start.command=/usr/bin/systemctl start tomcat
stop.command=/usr/bin/systemctl stop tomcat
incident.backup_on_pid_change=true
```

`tomcat.service`에 `Restart=on-failure`가 켜져 있으면 systemd가 sentinel보다 먼저 Tomcat을 재기동할 수 있습니다. 이 경우 sentinel은 다운 상태를 못 보고 지나갈 수 있으므로, `incident.backup_on_pid_change=true`로 새 PID를 감지했을 때 백업만 남기게 둡니다.

sentinel이 반드시 먼저 백업하고 재기동해야 한다면 `tomcat.service`의 자동 재시작을 끄거나, `RestartSec`를 `check.interval + down.debounce`보다 길게 둡니다. 반대로 systemd를 빠른 fallback으로 쓰려면 `Restart=on-failure`를 유지하고 PID 변경 백업을 켭니다.

기본 sentinel unit은 `User=tomcat` 기준이며 `catalina.sh` 직접 실행에 맞춰져 있습니다. `systemctl start tomcat`을 실행하려면 root로 실행하거나 제한된 sudo/polkit/wrapper 정책을 별도로 구성해야 하며, 이 경우 `NoNewPrivileges` 설정도 함께 조정해야 합니다.

## 수동 검증

한 번만 감시 사이클을 실행하려면:

```sh
tomcat-sentinel -config /etc/tomcat-sentinel/tomcat.properties -once
```

Docker 기반 Linux live smoke:

```sh
scripts/docker-live-smoke.sh
```

이 smoke test는 Linux 컨테이너 안에서 fake Netty/JVM 프로세스를 만들고 다음을 확인합니다.

- `/proc` 기반 PID 확인
- OOM 로그 분류
- 로그 백업과 `manifest.json` 생성
- `start.command` 실행
- 재기동된 프로세스 생존 확인

## 백업 구조

장애가 감지되면 `backup.dir` 아래에 incident 디렉터리가 만들어집니다.

```text
/var/backups/tomcat-sentinel/
  20260617T103055Z-oom-pid-12345/
    manifest.json
    catalina.out
    catalina.2026-06-17.log
```

`manifest.json`에는 감지 시각, PID, 분류 결과, 매칭된 로그 패턴, 백업된 파일 목록이 기록됩니다.

## 안전 제약

- Tomcat 로그를 삭제하거나 truncate하지 않습니다.
- 전체 로그 파일을 메모리에 올리지 않습니다.
- 정상 상태에서 외부 명령을 반복 실행하지 않는 구성이 기본입니다.
- 무한 재시작을 막기 위해 cooldown과 max attempts를 둡니다.
- PID가 예상 프로세스가 아니면 재기동하지 않습니다.
- 기본 systemd unit에는 `MemoryMax`, `CPUQuota`, `Nice`를 넣지 않습니다. `catalina.sh`로 JVM을 직접 띄우면 Tomcat이 같은 service cgroup 정책을 물려받을 수 있습니다.

## 주요 설정

| Key | 설명 |
| --- | --- |
| `pid.file` | 감시 대상 PID 파일 |
| `pid.command` | PID 파일이 없을 때 사용할 custom PID 명령 |
| `log.paths` | 장애 시점에 스캔할 로그 glob 목록 |
| `backup.paths` | incident에 복사할 로그 glob 목록. 비우면 `log.paths` 사용 |
| `backup.dir` | incident 백업 디렉터리 |
| `start.command` | 재기동 명령 |
| `stop.command` | 중지 명령 |
| `process.command_hint` | PID가 올바른 프로세스인지 확인할 cmdline hint |
| `check.interval` | 정상 상태 체크 주기 |
| `down.debounce` | down 감지 후 재확인 대기 시간 |
| `restart.max_attempts` | window 안에서 허용할 최대 재시작 횟수 |
| `restart.window` | 재시작 횟수 제한 기간 |
| `log.scan_tail_bytes` | 로그 파일당 tail scan 크기 |
| `log.scan_max_files` | 한 사이클에서 스캔할 최대 파일 수 |
| `incident.backup_on_pid_change` | 정상 실행 중 PID가 바뀌면 외부 재시작으로 보고 백업만 수행 |

환경변수 override는 `TOMCAT_SENTINEL_` prefix를 사용합니다.

예:

```sh
TOMCAT_SENTINEL_CHECK_INTERVAL=30s
TOMCAT_SENTINEL_START_COMMAND="/opt/tomcat/bin/catalina.sh start"
```

이전 이름에서 쓰던 `JVM_SENTINEL_` prefix도 호환됩니다.
