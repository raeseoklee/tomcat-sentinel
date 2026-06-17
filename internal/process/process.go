package process

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/raeseoklee/tomcat-sentinel/internal/command"
)

type Inspector interface {
	Inspect(pid int) (Info, error)
}

type Info struct {
	PID     int
	Alive   bool
	Zombie  bool
	Cmdline string
}

type ProcInspector struct {
	ProcRoot string
}

func (i ProcInspector) Inspect(pid int) (Info, error) {
	info := Info{PID: pid}
	if pid <= 0 {
		return info, fmt.Errorf("invalid pid %d", pid)
	}
	root := i.ProcRoot
	if root == "" {
		root = "/proc"
	}
	dir := filepath.Join(root, strconv.Itoa(pid))
	if _, err := os.Stat(dir); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			if procRootMissing(root) {
				alive, err := signalAlive(pid)
				if err != nil {
					return info, err
				}
				info.Alive = alive
				return info, nil
			}
			return info, nil
		}
		return info, err
	}
	info.Alive = true

	if stat, err := os.ReadFile(filepath.Join(dir, "stat")); err == nil {
		info.Zombie = parseProcState(stat) == "Z"
	}
	if cmdline, err := os.ReadFile(filepath.Join(dir, "cmdline")); err == nil {
		info.Cmdline = strings.TrimSpace(strings.ReplaceAll(string(cmdline), "\x00", " "))
	}
	return info, nil
}

func procRootMissing(root string) bool {
	_, err := os.Stat(root)
	return errors.Is(err, os.ErrNotExist)
}

func parseProcState(stat []byte) string {
	s := string(stat)
	closeParen := strings.LastIndex(s, ")")
	if closeParen < 0 || closeParen+2 >= len(s) {
		return ""
	}
	rest := strings.TrimSpace(s[closeParen+1:])
	if rest == "" {
		return ""
	}
	fields := strings.Fields(rest)
	if len(fields) == 0 {
		return ""
	}
	return fields[0]
}

type Resolver struct {
	PIDFile            string
	PIDCommand         string
	StatusCommand      string
	ProcessCommandHint string
	Inspector          Inspector
	Runner             command.Runner
}

type Check struct {
	PID     int
	Info    Info
	Alive   bool
	Trusted bool
	Reason  string
}

func (r Resolver) Check(ctx context.Context) (Check, error) {
	pid, reason, err := r.Resolve(ctx)
	if err != nil {
		return Check{Trusted: true, Reason: reason}, err
	}
	if pid <= 0 {
		return Check{Trusted: true, Reason: reason}, nil
	}
	inspector := r.Inspector
	if inspector == nil {
		inspector = ProcInspector{}
	}
	info, err := inspector.Inspect(pid)
	if err != nil {
		return Check{PID: pid, Trusted: false, Reason: "inspect-failed"}, err
	}
	check := Check{
		PID:     pid,
		Info:    info,
		Alive:   info.Alive && !info.Zombie,
		Trusted: true,
		Reason:  reason,
	}
	if !check.Alive {
		return check, nil
	}
	if r.ProcessCommandHint != "" && info.Cmdline != "" && !strings.Contains(info.Cmdline, r.ProcessCommandHint) {
		check.Trusted = false
		check.Reason = "unexpected-process"
	}
	return check, nil
}

func (r Resolver) Resolve(ctx context.Context) (int, string, error) {
	if strings.TrimSpace(r.PIDCommand) != "" {
		pid, err := r.pidFromCommand(ctx, r.PIDCommand)
		return pid, "pid.command", err
	}
	if strings.TrimSpace(r.PIDFile) != "" {
		pid, err := readPIDFile(r.PIDFile)
		if err == nil {
			return pid, "pid.file", nil
		}
		if !errors.Is(err, os.ErrNotExist) {
			return 0, "pid.file", err
		}
	}
	if strings.TrimSpace(r.StatusCommand) != "" {
		pid, err := r.pidFromCommand(ctx, r.StatusCommand)
		return pid, "status.command", err
	}
	return 0, "no-pid-source", nil
}

func (r Resolver) pidFromCommand(ctx context.Context, cmd string) (int, error) {
	result, err := r.Runner.Run(ctx, cmd)
	output := strings.TrimSpace(result.Stdout + "\n" + result.Stderr)
	if err != nil {
		return 0, fmt.Errorf("run pid command: %w", err)
	}
	return firstPID(output)
}

func readPIDFile(path string) (int, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return 0, err
	}
	return firstPID(string(data))
}

func firstPID(s string) (int, error) {
	for _, field := range strings.Fields(s) {
		pid, err := strconv.Atoi(field)
		if err == nil && pid > 0 {
			return pid, nil
		}
	}
	return 0, fmt.Errorf("no pid found")
}
