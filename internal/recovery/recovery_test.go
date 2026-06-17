package recovery

import (
	"bytes"
	"context"
	"encoding/json"
	"log"
	"os"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	"github.com/raeseoklee/tomcat-sentinel/internal/backup"
	"github.com/raeseoklee/tomcat-sentinel/internal/config"
	"github.com/raeseoklee/tomcat-sentinel/internal/logscan"
	"github.com/raeseoklee/tomcat-sentinel/internal/process"
)

func TestCheckOnceDoesNothingWhenProcessAlive(t *testing.T) {
	cfg, procRoot, _ := testConfig(t, true)
	var logs bytes.Buffer
	s := New(cfg, log.New(&logs, "", 0), "test")
	s.Resolver.Inspector = process.ProcInspector{ProcRoot: procRoot}
	s.sleep = noSleep

	result, err := s.CheckOnce(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if result.State != "running" || result.Restarted {
		t.Fatalf("result=%+v", result)
	}
}

func TestCheckOnceBacksUpAndRestartsWhenProcessDown(t *testing.T) {
	cfg, procRoot, marker := testConfig(t, false)
	var logs bytes.Buffer
	s := New(cfg, log.New(&logs, "", 0), "test")
	s.Resolver.Inspector = process.ProcInspector{ProcRoot: procRoot}
	s.sleep = noSleep

	result, err := s.CheckOnce(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if !result.Restarted || result.State != "restarted" {
		t.Fatalf("result=%+v logs=%s", result, logs.String())
	}
	if _, err := os.Stat(marker); err != nil {
		t.Fatalf("start marker not created: %v", err)
	}
	if result.BackupDir == "" {
		t.Fatal("expected backup dir")
	}
	if _, err := os.Stat(filepath.Join(result.BackupDir, "manifest.json")); err != nil {
		t.Fatalf("manifest missing: %v", err)
	}
}

func TestCheckOnceBacksUpWithoutRestartWhenPIDChanges(t *testing.T) {
	cfg, procRoot, marker := testConfig(t, true)
	var logs bytes.Buffer
	s := New(cfg, log.New(&logs, "", 0), "test")
	s.Resolver.Inspector = process.ProcInspector{ProcRoot: procRoot}
	s.sleep = noSleep

	if result, err := s.CheckOnce(context.Background()); err != nil || result.State != "running" {
		t.Fatalf("initial result=%+v err=%v", result, err)
	}

	if err := os.WriteFile(cfg.PIDFile, []byte("4243\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	writeFakeProc(t, procRoot, 4243)

	result, err := s.CheckOnce(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if result.State != "pid-changed" || result.Restarted {
		t.Fatalf("result=%+v logs=%s", result, logs.String())
	}
	if _, err := os.Stat(marker); !os.IsNotExist(err) {
		t.Fatalf("start marker should not exist after pid-change backup: %v", err)
	}
	manifestData, err := os.ReadFile(filepath.Join(result.BackupDir, "manifest.json"))
	if err != nil {
		t.Fatal(err)
	}
	var manifest backup.Manifest
	if err := json.Unmarshal(manifestData, &manifest); err != nil {
		t.Fatal(err)
	}
	if manifest.PID != 4242 || manifest.Classification != logscan.ClassificationOOM {
		t.Fatalf("manifest=%+v", manifest)
	}
}

func TestBackupLogsUsesBackupPathsWhenConfigured(t *testing.T) {
	cfg, _, _ := testConfig(t, true)
	dir := t.TempDir()
	scanLog := filepath.Join(dir, "scan.log")
	backupLog := filepath.Join(dir, "backup.log")
	if err := os.WriteFile(scanLog, []byte("scan"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(backupLog, []byte("backup"), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg.LogPaths = []string{scanLog, backupLog}
	cfg.Backup.Paths = []string{backupLog}
	cfg.Backup.Dir = filepath.Join(dir, "backup-dir")

	s := New(cfg, log.New(&bytes.Buffer{}, "", 0), "test")
	result, err := s.backupLogs(backup.Incident{
		Time:           time.Date(2026, 6, 17, 1, 2, 3, 0, time.UTC),
		Classification: logscan.ClassificationCrash,
		PID:            4242,
		Reason:         "test",
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Files) != 1 || result.Files[0].Source != backupLog {
		t.Fatalf("files=%+v", result.Files)
	}
}

func testConfig(t *testing.T, alive bool) (config.Config, string, string) {
	t.Helper()

	root := t.TempDir()
	tomcatHome := filepath.Join(root, "tomcat")
	tomcatBase := tomcatHome
	logDir := filepath.Join(tomcatBase, "logs")
	tempDir := filepath.Join(tomcatBase, "temp")
	if err := os.MkdirAll(logDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(tempDir, 0o755); err != nil {
		t.Fatal(err)
	}
	logPath := filepath.Join(logDir, "catalina.out")
	if err := os.WriteFile(logPath, []byte("java.lang.OutOfMemoryError: Java heap space\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	pidFile := filepath.Join(tempDir, "tomcat.pid")
	if err := os.WriteFile(pidFile, []byte("4242\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	procRoot := filepath.Join(root, "proc")
	if alive {
		writeFakeProc(t, procRoot, 4242)
	}

	marker := filepath.Join(root, "started")
	cfg := config.Default()
	cfg.TomcatHome = tomcatHome
	cfg.TomcatBase = tomcatBase
	cfg.App.Home = tomcatHome
	cfg.App.Base = tomcatBase
	cfg.PIDFile = pidFile
	cfg.LogPaths = []string{logPath}
	cfg.Backup.Dir = filepath.Join(root, "backup")
	cfg.Command.Start = "touch " + marker
	cfg.Command.Timeout = time.Second
	cfg.Runtime.DownDebounce = 0
	cfg.Runtime.StartupVerifyTimeout = time.Second
	cfg.Runtime.StartupVerifyInterval = time.Millisecond
	cfg.Restart.MinMemAvailableMB = 0
	cfg.Restart.Cooldown = 0
	cfg.Process.CommandHint = "org.apache.catalina.startup.Bootstrap"

	if !alive {
		// Start command creates fake /proc evidence so verification can pass.
		procDir := filepath.Join(procRoot, "4242")
		cfg.Command.Start = "mkdir -p " + procDir + " && printf '4242 (java) S 1 2 3' > " + filepath.Join(procDir, "stat") + " && printf 'java\\0org.apache.catalina.startup.Bootstrap\\0' > " + filepath.Join(procDir, "cmdline") + " && touch " + marker
	}
	return cfg, procRoot, marker
}

func writeFakeProc(t *testing.T, procRoot string, pid int) {
	t.Helper()

	procDir := filepath.Join(procRoot, strconv.Itoa(pid))
	if err := os.MkdirAll(procDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(procDir, "stat"), []byte(strconv.Itoa(pid)+" (java) S 1 2 3"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(procDir, "cmdline"), []byte("java\x00org.apache.catalina.startup.Bootstrap\x00"), 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestNewUsesGenericAppEnvForNetty(t *testing.T) {
	cfg := config.Default()
	cfg.App.Name = "netty-api"
	cfg.App.Kind = "netty"
	cfg.App.Home = "/srv/netty-api"
	cfg.App.Base = "/var/lib/netty-api"
	cfg.Command.Env = []string{"JAVA_OPTS=-Xmx256m"}

	s := New(cfg, log.New(&bytes.Buffer{}, "", 0), "test")
	if s.Runner.Dir != "/srv/netty-api" {
		t.Fatalf("Runner.Dir=%q", s.Runner.Dir)
	}
	if !containsEnv(s.Runner.Env, "APP_KIND=netty") {
		t.Fatalf("missing APP_KIND env: %#v", s.Runner.Env)
	}
	if containsEnvPrefix(s.Runner.Env, "CATALINA_HOME=") {
		t.Fatalf("unexpected CATALINA env for netty: %#v", s.Runner.Env)
	}
	if !containsEnv(s.Runner.Env, "JAVA_OPTS=-Xmx256m") {
		t.Fatalf("missing custom env: %#v", s.Runner.Env)
	}
}

func containsEnv(env []string, needle string) bool {
	for _, item := range env {
		if item == needle {
			return true
		}
	}
	return false
}

func containsEnvPrefix(env []string, prefix string) bool {
	for _, item := range env {
		if len(item) >= len(prefix) && item[:len(prefix)] == prefix {
			return true
		}
	}
	return false
}

func noSleep(ctx context.Context, _ time.Duration) error {
	select {
	case <-ctx.Done():
		return ctx.Err()
	default:
		return nil
	}
}
