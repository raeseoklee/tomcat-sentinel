package recovery

import (
	"context"
	"fmt"
	"log"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/raeseoklee/tomcat-sentinel/internal/backup"
	"github.com/raeseoklee/tomcat-sentinel/internal/command"
	"github.com/raeseoklee/tomcat-sentinel/internal/config"
	"github.com/raeseoklee/tomcat-sentinel/internal/logscan"
	"github.com/raeseoklee/tomcat-sentinel/internal/process"
)

type Sentinel struct {
	Config   config.Config
	Logger   *log.Logger
	Version  string
	Resolver process.Resolver
	Runner   command.Runner

	attempts []time.Time
	now      func() time.Time
	sleep    func(context.Context, time.Duration) error
}

type CheckResult struct {
	State          string
	Classification string
	Restarted      bool
	BackupDir      string
	Message        string
}

func New(cfg config.Config, logger *log.Logger, version string) *Sentinel {
	env := []string{
		"APP_NAME=" + cfg.App.Name,
		"APP_KIND=" + cfg.App.Kind,
		"APP_HOME=" + cfg.App.Home,
		"APP_BASE=" + cfg.App.Base,
	}
	if cfg.App.Kind == "tomcat" {
		env = append(env,
			"CATALINA_HOME="+cfg.App.Home,
			"CATALINA_BASE="+cfg.App.Base,
			"CATALINA_PID="+cfg.PIDFile,
		)
	}
	env = append(env, cfg.Command.Env...)
	runner := command.Runner{
		Timeout:        cfg.Command.Timeout,
		OutputMaxBytes: cfg.Command.OutputMaxBytes,
		Dir:            cfg.App.Home,
		Env:            env,
	}
	resolver := process.Resolver{
		PIDFile:            cfg.PIDFile,
		PIDCommand:         cfg.PIDCommand,
		StatusCommand:      cfg.StatusCommand,
		ProcessCommandHint: cfg.Process.CommandHint,
		Inspector:          process.ProcInspector{},
		Runner:             runner,
	}
	if logger == nil {
		logger = log.New(os.Stdout, "", log.LstdFlags)
	}
	return &Sentinel{
		Config:   cfg,
		Logger:   logger,
		Version:  version,
		Resolver: resolver,
		Runner:   runner,
		now:      time.Now,
		sleep:    sleepContext,
	}
}

func (s *Sentinel) Run(ctx context.Context) error {
	ticker := time.NewTicker(s.Config.Runtime.CheckInterval)
	defer ticker.Stop()

	s.Logger.Printf("sentinel started profile=%s check_interval=%s", s.Config.Resource.Profile, s.Config.Runtime.CheckInterval)
	for {
		result, err := s.CheckOnce(ctx)
		if err != nil {
			s.Logger.Printf("check failed state=%s message=%q error=%v", result.State, result.Message, err)
		} else if result.Message != "" {
			s.Logger.Printf("check state=%s classification=%s restarted=%t message=%q", result.State, result.Classification, result.Restarted, result.Message)
		}

		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
		}
	}
}

func (s *Sentinel) CheckOnce(ctx context.Context) (CheckResult, error) {
	check, err := s.Resolver.Check(ctx)
	if err != nil {
		return CheckResult{State: "unknown", Message: "pid check failed"}, err
	}
	if check.Alive && check.Trusted {
		return CheckResult{State: "running", Message: fmt.Sprintf("pid %d alive", check.PID)}, nil
	}
	if check.Alive && !check.Trusted {
		if !s.Config.Restart.OnUnknown {
			return CheckResult{
				State:          "unknown",
				Classification: "unknown",
				Message:        fmt.Sprintf("pid %d is alive but command hint did not match", check.PID),
			}, nil
		}
		return s.recover(ctx, check, "unknown", nil)
	}

	if s.Config.Runtime.DownDebounce > 0 {
		if err := s.sleep(ctx, s.Config.Runtime.DownDebounce); err != nil {
			return CheckResult{State: "cancelled", Message: "cancelled during debounce"}, err
		}
		confirm, err := s.Resolver.Check(ctx)
		if err != nil {
			return CheckResult{State: "unknown", Message: "pid confirm failed"}, err
		}
		if confirm.Alive && confirm.Trusted {
			return CheckResult{State: "running", Message: fmt.Sprintf("pid %d alive after debounce", confirm.PID)}, nil
		}
		check = confirm
	}

	scanReport, err := s.scanLogs()
	if err != nil {
		return CheckResult{State: "down", Message: "log scan failed"}, err
	}
	return s.recover(ctx, check, scanReport.Classification, scanReport.Evidence)
}

func (s *Sentinel) recover(ctx context.Context, check process.Check, classification string, evidence []logscan.Evidence) (CheckResult, error) {
	result := CheckResult{State: "down", Classification: classification}
	if classification == "" {
		classification = logscan.ClassificationCrash
		result.Classification = classification
	}
	if classification == "unknown" && !s.Config.Restart.OnUnknown {
		result.Message = "unknown process state; restart disabled"
		return result, nil
	}
	if !s.allowAttempt() {
		result.State = "rate-limited"
		result.Message = "restart attempt limit reached"
		return result, nil
	}
	if s.Config.Restart.MinMemAvailableMB > 0 {
		available, err := memAvailableMB()
		if err == nil && available >= 0 && available < s.Config.Restart.MinMemAvailableMB {
			result.State = "waiting-memory"
			result.Message = fmt.Sprintf("MemAvailable=%dMiB below threshold=%dMiB", available, s.Config.Restart.MinMemAvailableMB)
			return result, nil
		}
	}

	incident := backup.Incident{
		Time:           s.now(),
		Classification: classification,
		PID:            check.PID,
		Reason:         check.Reason,
		Evidence:       evidence,
	}
	backupResult, backupErr := s.backupLogs(incident)
	if backupErr != nil {
		result.Message = "backup failed"
		if !s.Config.Restart.WhenBackupFails {
			return result, backupErr
		}
		s.Logger.Printf("backup failed but restart is enabled error=%v", backupErr)
	} else {
		result.BackupDir = backupResult.Dir
	}

	s.recordAttempt()
	startResult, err := s.Runner.Run(ctx, s.Config.Command.Start)
	if err != nil {
		result.State = "restart-failed"
		result.Message = fmt.Sprintf("start command failed exit=%d stderr=%q", startResult.ExitCode, trimForLog(startResult.Stderr))
		if backupErr != nil {
			return result, fmt.Errorf("backup failed: %v; start failed: %w", backupErr, err)
		}
		return result, err
	}

	if err := s.verifyRunning(ctx); err != nil {
		result.State = "verify-failed"
		result.Message = "start command succeeded but pid did not become healthy"
		return result, err
	}
	result.State = "restarted"
	result.Restarted = true
	result.Message = cfgName(s.Config) + " restarted"
	return result, backupErr
}

func (s *Sentinel) scanLogs() (logscan.Report, error) {
	scanner := logscan.Scanner{
		Paths:            s.Config.LogPaths,
		TailBytes:        s.Config.LogScan.TailBytes,
		MaxFiles:         s.Config.LogScan.MaxFiles,
		OOMPatterns:      s.Config.Patterns.OOM,
		ShutdownPatterns: s.Config.Patterns.Shutdown,
	}
	return scanner.Scan()
}

func (s *Sentinel) backupLogs(incident backup.Incident) (backup.Result, error) {
	manager := backup.Manager{
		LogPaths:        s.Config.LogPaths,
		Dir:             s.Config.Backup.Dir,
		MaxBytesPerFile: s.Config.Backup.MaxBytesPerFile,
		CopyBufferBytes: s.Config.Backup.CopyBufferBytes,
		RetentionDays:   s.Config.Backup.RetentionDays,
		Version:         s.Version,
	}
	return manager.Backup(incident)
}

func (s *Sentinel) verifyRunning(ctx context.Context) error {
	deadline := s.now().Add(s.Config.Runtime.StartupVerifyTimeout)
	for {
		check, err := s.Resolver.Check(ctx)
		if err == nil && check.Alive && check.Trusted {
			return nil
		}
		if !s.now().Before(deadline) {
			if err != nil {
				return err
			}
			return fmt.Errorf("%s did not become healthy before timeout", cfgName(s.Config))
		}
		if err := s.sleep(ctx, s.Config.Runtime.StartupVerifyInterval); err != nil {
			return err
		}
	}
}

func cfgName(cfg config.Config) string {
	if cfg.App.Name != "" {
		return cfg.App.Name
	}
	if cfg.App.Kind != "" {
		return cfg.App.Kind
	}
	return "app"
}

func (s *Sentinel) allowAttempt() bool {
	now := s.now()
	windowStart := now.Add(-s.Config.Restart.Window)
	filtered := s.attempts[:0]
	for _, attempt := range s.attempts {
		if attempt.After(windowStart) {
			filtered = append(filtered, attempt)
		}
	}
	s.attempts = filtered
	if len(s.attempts) >= s.Config.Restart.MaxAttempts {
		return false
	}
	if len(s.attempts) > 0 && now.Sub(s.attempts[len(s.attempts)-1]) < s.Config.Restart.Cooldown {
		return false
	}
	return true
}

func (s *Sentinel) recordAttempt() {
	s.attempts = append(s.attempts, s.now())
}

func memAvailableMB() (int, error) {
	data, err := os.ReadFile("/proc/meminfo")
	if err != nil {
		if os.IsNotExist(err) {
			return -1, nil
		}
		return -1, err
	}
	for _, line := range strings.Split(string(data), "\n") {
		fields := strings.Fields(line)
		if len(fields) >= 2 && fields[0] == "MemAvailable:" {
			kb, err := strconv.Atoi(fields[1])
			if err != nil {
				return -1, err
			}
			return kb / 1024, nil
		}
	}
	return -1, nil
}

func sleepContext(ctx context.Context, d time.Duration) error {
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

func trimForLog(s string) string {
	s = strings.TrimSpace(s)
	if len(s) > 256 {
		return s[:256]
	}
	return s
}
