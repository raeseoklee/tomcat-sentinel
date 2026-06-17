package recovery

import (
	"bytes"
	"context"
	"log"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/raeseoklee/jvm-sentinel/internal/config"
	"github.com/raeseoklee/jvm-sentinel/internal/process"
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

	pid := 4242
	pidFile := filepath.Join(tempDir, "tomcat.pid")
	if err := os.WriteFile(pidFile, []byte("4242\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	procRoot := filepath.Join(root, "proc")
	if alive {
		procDir := filepath.Join(procRoot, "4242")
		if err := os.MkdirAll(procDir, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(procDir, "stat"), []byte("4242 (java) S 1 2 3"), 0o600); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(procDir, "cmdline"), []byte("java\x00org.apache.catalina.startup.Bootstrap\x00"), 0o600); err != nil {
			t.Fatal(err)
		}
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
	_ = pid
	return cfg, procRoot, marker
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
