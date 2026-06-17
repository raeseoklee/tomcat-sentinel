package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"log"
	"os"
	"os/signal"
	"syscall"

	"github.com/raeseoklee/tomcat-sentinel/internal/config"
	"github.com/raeseoklee/tomcat-sentinel/internal/recovery"
)

var version = "dev"

func main() {
	if err := run(); err != nil {
		fmt.Fprintf(os.Stderr, "tomcat-sentinel: %v\n", err)
		os.Exit(1)
	}
}

func run() error {
	var configPath string
	var once bool
	var showVersion bool

	defaultConfigPath := os.Getenv(config.EnvPrefix + "CONFIG")
	if defaultConfigPath == "" {
		defaultConfigPath = os.Getenv(config.LegacyEnvPrefix + "CONFIG")
	}
	flag.StringVar(&configPath, "config", defaultConfigPath, "config file path")
	flag.BoolVar(&once, "once", false, "run one check cycle and exit")
	flag.BoolVar(&showVersion, "version", false, "print version and exit")
	flag.Parse()

	if showVersion {
		fmt.Println(version)
		return nil
	}

	cfg, err := config.Load(configPath)
	if err != nil {
		return err
	}
	logger, closeLogger, err := newLogger(cfg)
	if err != nil {
		return err
	}
	defer closeLogger()

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	s := recovery.New(cfg, logger, version)
	if once {
		result, err := s.CheckOnce(ctx)
		logger.Printf("once state=%s classification=%s restarted=%t backup_dir=%q message=%q", result.State, result.Classification, result.Restarted, result.BackupDir, result.Message)
		return err
	}

	err = s.Run(ctx)
	if errors.Is(err, context.Canceled) {
		return nil
	}
	return err
}

func newLogger(cfg config.Config) (*log.Logger, func(), error) {
	if cfg.Sentinel.LogFile == "" {
		return log.New(os.Stdout, "", log.LstdFlags), func() {}, nil
	}
	f, err := os.OpenFile(cfg.Sentinel.LogFile, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o640)
	if err != nil {
		return nil, nil, err
	}
	writer := io.MultiWriter(os.Stdout, f)
	return log.New(writer, "", log.LstdFlags), func() { _ = f.Close() }, nil
}
