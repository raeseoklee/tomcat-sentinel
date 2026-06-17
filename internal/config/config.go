package config

import (
	"bufio"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"
)

const (
	EnvPrefix       = "TOMCAT_SENTINEL_"
	LegacyEnvPrefix = "JVM_SENTINEL_"
)

var knownKeys = []string{
	"tomcat.home",
	"tomcat.base",
	"app.name",
	"app.kind",
	"app.home",
	"app.base",
	"pid.file",
	"pid.command",
	"status.command",
	"log.paths",
	"backup.paths",
	"backup.dir",
	"backup.max_bytes_per_file",
	"backup.copy_buffer_bytes",
	"backup.retention.days",
	"start.command",
	"stop.command",
	"command.env",
	"command.timeout",
	"command.output_max_bytes",
	"log.scan_tail_bytes",
	"log.scan_max_files",
	"check.interval",
	"down.debounce",
	"startup.verify.timeout",
	"startup.verify.interval",
	"restart.cooldown",
	"restart.max_attempts",
	"restart.window",
	"restart.on_unknown",
	"restart.when_backup_fails",
	"restart.min_mem_available_mb",
	"incident.backup_on_pid_change",
	"resource.profile",
	"resource.soft_rss_limit_mb",
	"resource.compression_enabled",
	"process.command_hint",
	"oom.patterns",
	"shutdown.patterns",
	"sentinel.log.file",
	"sentinel.log.level",
}

type Config struct {
	App AppConfig

	// TomcatHome and TomcatBase are compatibility aliases for older configs.
	TomcatHome string
	TomcatBase string

	PIDFile       string
	PIDCommand    string
	StatusCommand string

	LogPaths []string
	Backup   BackupConfig
	Command  CommandConfig
	LogScan  LogScanConfig
	Runtime  RuntimeConfig
	Restart  RestartConfig
	Incident IncidentConfig
	Resource ResourceConfig
	Process  ProcessConfig
	Patterns PatternConfig
	Sentinel SentinelConfig
}

type AppConfig struct {
	Name string
	Kind string
	Home string
	Base string
}

type BackupConfig struct {
	Paths           []string
	Dir             string
	MaxBytesPerFile int64
	CopyBufferBytes int
	RetentionDays   int
}

type CommandConfig struct {
	Start          string
	Stop           string
	Env            []string
	Timeout        time.Duration
	OutputMaxBytes int64
}

type LogScanConfig struct {
	TailBytes int64
	MaxFiles  int
}

type RuntimeConfig struct {
	CheckInterval         time.Duration
	DownDebounce          time.Duration
	StartupVerifyTimeout  time.Duration
	StartupVerifyInterval time.Duration
}

type RestartConfig struct {
	Cooldown          time.Duration
	MaxAttempts       int
	Window            time.Duration
	OnUnknown         bool
	WhenBackupFails   bool
	MinMemAvailableMB int
}

type IncidentConfig struct {
	BackupOnPIDChange bool
}

type ResourceConfig struct {
	Profile            string
	SoftRSSLimitMB     int
	CompressionEnabled bool
}

type ProcessConfig struct {
	CommandHint string
}

type PatternConfig struct {
	OOM      []string
	Shutdown []string
}

type SentinelConfig struct {
	LogFile  string
	LogLevel string
}

func Default() Config {
	return Config{
		App: AppConfig{
			Name: "tomcat",
			Kind: "tomcat",
			Home: "/opt/tomcat",
			Base: "/opt/tomcat",
		},
		TomcatHome: "/opt/tomcat",
		TomcatBase: "/opt/tomcat",
		PIDFile:    "/opt/tomcat/temp/tomcat.pid",
		LogPaths: []string{
			"/opt/tomcat/logs/catalina.out",
			"/opt/tomcat/logs/catalina.*.log",
			"/opt/tomcat/logs/localhost.*.log",
		},
		Backup: BackupConfig{
			Dir:             "/var/backups/tomcat-sentinel",
			MaxBytesPerFile: 8 * 1024 * 1024,
			CopyBufferBytes: 32 * 1024,
			RetentionDays:   7,
		},
		Command: CommandConfig{
			Start:          "/opt/tomcat/bin/catalina.sh start",
			Stop:           "/opt/tomcat/bin/catalina.sh stop",
			Timeout:        time.Minute,
			OutputMaxBytes: 32 * 1024,
		},
		LogScan: LogScanConfig{
			TailBytes: 512 * 1024,
			MaxFiles:  4,
		},
		Runtime: RuntimeConfig{
			CheckInterval:         15 * time.Second,
			DownDebounce:          3 * time.Second,
			StartupVerifyTimeout:  90 * time.Second,
			StartupVerifyInterval: 2 * time.Second,
		},
		Restart: RestartConfig{
			Cooldown:          30 * time.Second,
			MaxAttempts:       3,
			Window:            10 * time.Minute,
			OnUnknown:         false,
			WhenBackupFails:   true,
			MinMemAvailableMB: 32,
		},
		Incident: IncidentConfig{
			BackupOnPIDChange: true,
		},
		Resource: ResourceConfig{
			Profile:            "tiny-1vcpu-512m",
			SoftRSSLimitMB:     24,
			CompressionEnabled: false,
		},
		Process: ProcessConfig{
			CommandHint: "org.apache.catalina.startup.Bootstrap",
		},
		Patterns: PatternConfig{
			OOM: []string{
				"java.lang.OutOfMemoryError",
				"GC overhead limit exceeded",
				"Java heap space",
				"Metaspace",
				"unable to create native thread",
				"Killed",
				"Out of memory: Kill process",
			},
			Shutdown: []string{
				"Destroying ProtocolHandler",
				"Stopping service [Catalina]",
				"SEVERE",
			},
		},
		Sentinel: SentinelConfig{
			LogLevel: "info",
		},
	}
}

func Load(path string) (Config, error) {
	cfg := Default()
	props := map[string]string{}

	if path != "" {
		fileProps, err := readProperties(path)
		if err != nil {
			return Config{}, err
		}
		for key, value := range fileProps {
			props[key] = value
		}
	}

	applyEnv(props)
	if err := applyProperties(&cfg, props); err != nil {
		return Config{}, err
	}
	expandConfig(&cfg)
	return cfg, cfg.Validate()
}

func (c Config) Validate() error {
	var missing []string
	if c.App.Home == "" {
		missing = append(missing, "app.home")
	}
	if c.App.Base == "" {
		missing = append(missing, "app.base")
	}
	if c.PIDFile == "" && c.PIDCommand == "" {
		missing = append(missing, "pid.file or pid.command")
	}
	if len(c.LogPaths) == 0 {
		missing = append(missing, "log.paths")
	}
	if c.Backup.Dir == "" {
		missing = append(missing, "backup.dir")
	}
	if c.Command.Start == "" {
		missing = append(missing, "start.command")
	}
	if len(missing) > 0 {
		return fmt.Errorf("missing required config: %s", strings.Join(missing, ", "))
	}
	if c.Backup.MaxBytesPerFile <= 0 {
		return fmt.Errorf("backup.max_bytes_per_file must be positive")
	}
	if c.Backup.CopyBufferBytes <= 0 {
		return fmt.Errorf("backup.copy_buffer_bytes must be positive")
	}
	if c.Command.OutputMaxBytes <= 0 {
		return fmt.Errorf("command.output_max_bytes must be positive")
	}
	if c.LogScan.TailBytes <= 0 {
		return fmt.Errorf("log.scan_tail_bytes must be positive")
	}
	if c.LogScan.MaxFiles <= 0 {
		return fmt.Errorf("log.scan_max_files must be positive")
	}
	if c.Runtime.CheckInterval <= 0 {
		return fmt.Errorf("check.interval must be positive")
	}
	if c.Runtime.StartupVerifyInterval <= 0 {
		return fmt.Errorf("startup.verify.interval must be positive")
	}
	if c.Restart.MaxAttempts <= 0 {
		return fmt.Errorf("restart.max_attempts must be positive")
	}
	if c.Restart.Window <= 0 {
		return fmt.Errorf("restart.window must be positive")
	}
	return nil
}

func readProperties(path string) (map[string]string, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("open config %s: %w", path, err)
	}
	defer f.Close()

	props := map[string]string{}
	scanner := bufio.NewScanner(f)
	lineNo := 0
	for scanner.Scan() {
		lineNo++
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") || strings.HasPrefix(line, ";") {
			continue
		}
		key, value, ok := strings.Cut(line, "=")
		if !ok {
			return nil, fmt.Errorf("%s:%d: expected key=value", path, lineNo)
		}
		key = strings.TrimSpace(key)
		value = strings.TrimSpace(value)
		if key == "" {
			return nil, fmt.Errorf("%s:%d: empty key", path, lineNo)
		}
		props[key] = value
	}
	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("read config %s: %w", path, err)
	}
	return props, nil
}

func applyEnv(props map[string]string) {
	for _, key := range knownKeys {
		if value, ok := os.LookupEnv(legacyEnvName(key)); ok {
			props[key] = value
		}
		if value, ok := os.LookupEnv(envName(key)); ok {
			props[key] = value
		}
	}
}

func envName(key string) string {
	return EnvPrefix + strings.ToUpper(strings.ReplaceAll(key, ".", "_"))
}

func legacyEnvName(key string) string {
	return LegacyEnvPrefix + strings.ToUpper(strings.ReplaceAll(key, ".", "_"))
}

func applyProperties(cfg *Config, props map[string]string) error {
	for _, key := range knownKeys {
		value, ok := props[key]
		if !ok {
			continue
		}
		var err error
		switch key {
		case "app.name":
			cfg.App.Name = value
		case "app.kind":
			cfg.App.Kind = value
		case "app.home":
			cfg.App.Home = value
			cfg.TomcatHome = value
		case "app.base":
			cfg.App.Base = value
			cfg.TomcatBase = value
		case "tomcat.home":
			cfg.App.Home = value
			cfg.TomcatHome = value
		case "tomcat.base":
			cfg.App.Base = value
			cfg.TomcatBase = value
		case "pid.file":
			cfg.PIDFile = value
		case "pid.command":
			cfg.PIDCommand = value
		case "status.command":
			cfg.StatusCommand = value
		case "log.paths":
			cfg.LogPaths = csv(value)
		case "backup.paths":
			cfg.Backup.Paths = csv(value)
		case "backup.dir":
			cfg.Backup.Dir = value
		case "backup.max_bytes_per_file":
			cfg.Backup.MaxBytesPerFile, err = parseInt64(value)
		case "backup.copy_buffer_bytes":
			cfg.Backup.CopyBufferBytes, err = parseInt(value)
		case "backup.retention.days":
			cfg.Backup.RetentionDays, err = parseInt(value)
		case "start.command":
			cfg.Command.Start = value
		case "stop.command":
			cfg.Command.Stop = value
		case "command.env":
			cfg.Command.Env = csv(value)
		case "command.timeout":
			cfg.Command.Timeout, err = time.ParseDuration(value)
		case "command.output_max_bytes":
			cfg.Command.OutputMaxBytes, err = parseInt64(value)
		case "log.scan_tail_bytes":
			cfg.LogScan.TailBytes, err = parseInt64(value)
		case "log.scan_max_files":
			cfg.LogScan.MaxFiles, err = parseInt(value)
		case "check.interval":
			cfg.Runtime.CheckInterval, err = time.ParseDuration(value)
		case "down.debounce":
			cfg.Runtime.DownDebounce, err = time.ParseDuration(value)
		case "startup.verify.timeout":
			cfg.Runtime.StartupVerifyTimeout, err = time.ParseDuration(value)
		case "startup.verify.interval":
			cfg.Runtime.StartupVerifyInterval, err = time.ParseDuration(value)
		case "restart.cooldown":
			cfg.Restart.Cooldown, err = time.ParseDuration(value)
		case "restart.max_attempts":
			cfg.Restart.MaxAttempts, err = parseInt(value)
		case "restart.window":
			cfg.Restart.Window, err = time.ParseDuration(value)
		case "restart.on_unknown":
			cfg.Restart.OnUnknown, err = strconv.ParseBool(value)
		case "restart.when_backup_fails":
			cfg.Restart.WhenBackupFails, err = strconv.ParseBool(value)
		case "restart.min_mem_available_mb":
			cfg.Restart.MinMemAvailableMB, err = parseInt(value)
		case "incident.backup_on_pid_change":
			cfg.Incident.BackupOnPIDChange, err = strconv.ParseBool(value)
		case "resource.profile":
			cfg.Resource.Profile = value
		case "resource.soft_rss_limit_mb":
			cfg.Resource.SoftRSSLimitMB, err = parseInt(value)
		case "resource.compression_enabled":
			cfg.Resource.CompressionEnabled, err = strconv.ParseBool(value)
		case "process.command_hint":
			cfg.Process.CommandHint = value
		case "oom.patterns":
			cfg.Patterns.OOM = csv(value)
		case "shutdown.patterns":
			cfg.Patterns.Shutdown = csv(value)
		case "sentinel.log.file":
			cfg.Sentinel.LogFile = value
		case "sentinel.log.level":
			cfg.Sentinel.LogLevel = value
		}
		if err != nil {
			return fmt.Errorf("invalid %s=%q: %w", key, value, err)
		}
	}
	return nil
}

func expandConfig(cfg *Config) {
	if cfg.App.Name == "" {
		cfg.App.Name = cfg.App.Kind
	}
	if cfg.App.Kind == "" {
		cfg.App.Kind = "generic"
	}
	cfg.TomcatHome = cfg.App.Home
	cfg.TomcatBase = cfg.App.Base

	replacer := strings.NewReplacer(
		"${app.name}", cfg.App.Name,
		"${app.kind}", cfg.App.Kind,
		"${app.home}", cfg.App.Home,
		"${app.base}", cfg.App.Base,
		"${tomcat.home}", cfg.App.Home,
		"${tomcat.base}", cfg.App.Base,
	)
	cfg.PIDFile = replacer.Replace(cfg.PIDFile)
	cfg.PIDCommand = replacer.Replace(cfg.PIDCommand)
	cfg.StatusCommand = replacer.Replace(cfg.StatusCommand)
	cfg.Backup.Dir = replacer.Replace(cfg.Backup.Dir)
	cfg.Command.Start = replacer.Replace(cfg.Command.Start)
	cfg.Command.Stop = replacer.Replace(cfg.Command.Stop)
	for i := range cfg.LogPaths {
		cfg.LogPaths[i] = replacer.Replace(cfg.LogPaths[i])
	}
	for i := range cfg.Backup.Paths {
		cfg.Backup.Paths[i] = replacer.Replace(cfg.Backup.Paths[i])
	}
	for i := range cfg.Command.Env {
		cfg.Command.Env[i] = replacer.Replace(cfg.Command.Env[i])
	}
}

func csv(value string) []string {
	if strings.TrimSpace(value) == "" {
		return nil
	}
	parts := strings.Split(value, ",")
	out := make([]string, 0, len(parts))
	for _, part := range parts {
		part = strings.TrimSpace(part)
		if part != "" {
			out = append(out, part)
		}
	}
	return out
}

func parseInt(value string) (int, error) {
	i, err := strconv.Atoi(value)
	return i, err
}

func parseInt64(value string) (int64, error) {
	i, err := strconv.ParseInt(value, 10, 64)
	return i, err
}
