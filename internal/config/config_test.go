package config

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestLoadAppliesPropertiesAndEnv(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "sentinel.properties")
	data := []byte(`
tomcat.home=/srv/tomcat
tomcat.base=/srv/tomcat/base
pid.file=${app.base}/run/tomcat.pid
log.paths=${app.base}/logs/catalina.out,${tomcat.base}/logs/catalina.*.log
backup.dir=/backup
check.interval=30s
log.scan_tail_bytes=4096
log.scan_max_files=2
command.env=JAVA_HOME=/jvm,APP_LOG_DIR=${app.base}/logs
`)
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("TOMCAT_SENTINEL_CHECK_INTERVAL", "30s")
	t.Setenv("JVM_SENTINEL_CHECK_INTERVAL", "45s")
	t.Setenv("JVM_SENTINEL_BACKUP_MAX_BYTES_PER_FILE", "12345")

	cfg, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.TomcatHome != "/srv/tomcat" {
		t.Fatalf("TomcatHome=%q", cfg.TomcatHome)
	}
	if cfg.App.Home != "/srv/tomcat" || cfg.App.Base != "/srv/tomcat/base" {
		t.Fatalf("App=%+v", cfg.App)
	}
	if cfg.PIDFile != "/srv/tomcat/base/run/tomcat.pid" {
		t.Fatalf("PIDFile=%q", cfg.PIDFile)
	}
	if cfg.Runtime.CheckInterval != 45*time.Second {
		t.Fatalf("CheckInterval=%s", cfg.Runtime.CheckInterval)
	}
	if cfg.LogScan.TailBytes != 4096 || cfg.LogScan.MaxFiles != 2 {
		t.Fatalf("LogScan=%+v", cfg.LogScan)
	}
	if cfg.Backup.MaxBytesPerFile != 12345 {
		t.Fatalf("Backup.MaxBytesPerFile=%d", cfg.Backup.MaxBytesPerFile)
	}
	if got := cfg.Command.Env[1]; got != "APP_LOG_DIR=/srv/tomcat/base/logs" {
		t.Fatalf("Command.Env[1]=%q", got)
	}
}

func TestLoadSupportsGenericAppKeys(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "netty.properties")
	data := []byte(`
app.name=netty-api
app.kind=netty
app.home=/srv/netty-api
app.base=/var/lib/netty-api
pid.file=${app.base}/run/app.pid
log.paths=${app.base}/logs/app.log
backup.dir=/backup
start.command=${app.home}/bin/start.sh
process.command_hint=com.example.netty.Main
`)
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.App.Name != "netty-api" || cfg.App.Kind != "netty" {
		t.Fatalf("App=%+v", cfg.App)
	}
	if cfg.PIDFile != "/var/lib/netty-api/run/app.pid" {
		t.Fatalf("PIDFile=%q", cfg.PIDFile)
	}
	if cfg.Command.Start != "/srv/netty-api/bin/start.sh" {
		t.Fatalf("Start=%q", cfg.Command.Start)
	}
}

func TestAppKeysOverrideTomcatCompatibilityAliases(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "mixed.properties")
	data := []byte(`
tomcat.home=/opt/tomcat
tomcat.base=/opt/tomcat
app.name=netty-api
app.kind=netty
app.home=/srv/netty-api
app.base=/var/lib/netty-api
pid.file=${app.base}/run/app.pid
log.paths=${app.base}/logs/app.log
backup.dir=/backup
start.command=${app.home}/bin/start.sh
`)
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.App.Home != "/srv/netty-api" || cfg.App.Base != "/var/lib/netty-api" {
		t.Fatalf("App=%+v", cfg.App)
	}
	if cfg.TomcatHome != cfg.App.Home || cfg.TomcatBase != cfg.App.Base {
		t.Fatalf("Tomcat aliases not synced: home=%q base=%q app=%+v", cfg.TomcatHome, cfg.TomcatBase, cfg.App)
	}
}

func TestLoadDefaultsValidate(t *testing.T) {
	cfg, err := Load("")
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Resource.Profile != "tiny-1vcpu-512m" {
		t.Fatalf("Resource.Profile=%q", cfg.Resource.Profile)
	}
	if cfg.LogScan.TailBytes != 512*1024 {
		t.Fatalf("LogScan.TailBytes=%d", cfg.LogScan.TailBytes)
	}
}
