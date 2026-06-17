package command

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"sync"
	"time"
)

type Runner struct {
	Timeout        time.Duration
	OutputMaxBytes int64
	Dir            string
	Env            []string
}

type Result struct {
	Command         string
	ExitCode        int
	Stdout          string
	Stderr          string
	StdoutTruncated bool
	StderrTruncated bool
	TimedOut        bool
	Duration        time.Duration
}

func (r Runner) Run(ctx context.Context, command string) (Result, error) {
	result := Result{Command: command, ExitCode: -1}
	command = strings.TrimSpace(command)
	if command == "" {
		return result, fmt.Errorf("empty command")
	}

	timeout := r.Timeout
	if timeout <= 0 {
		timeout = time.Minute
	}
	if r.OutputMaxBytes <= 0 {
		r.OutputMaxBytes = 32 * 1024
	}

	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	started := time.Now()
	cmd := exec.CommandContext(ctx, "/bin/sh", "-c", command)
	prepareCommand(cmd)
	if r.Dir != "" {
		cmd.Dir = r.Dir
	}
	cmd.Env = append(os.Environ(), r.Env...)

	stdout := newLimitBuffer(r.OutputMaxBytes)
	stderr := newLimitBuffer(r.OutputMaxBytes)
	cmd.Stdout = stdout
	cmd.Stderr = stderr

	err := cmd.Run()
	result.Duration = time.Since(started)
	result.Stdout = stdout.String()
	result.Stderr = stderr.String()
	result.StdoutTruncated = stdout.Truncated()
	result.StderrTruncated = stderr.Truncated()
	result.TimedOut = errors.Is(ctx.Err(), context.DeadlineExceeded)
	if cmd.ProcessState != nil {
		result.ExitCode = cmd.ProcessState.ExitCode()
	}
	if result.TimedOut {
		killCommandGroup(cmd)
		return result, fmt.Errorf("command timed out after %s", timeout)
	}
	if err != nil {
		return result, err
	}
	return result, nil
}

type limitBuffer struct {
	mu        sync.Mutex
	buf       bytes.Buffer
	limit     int64
	written   int64
	truncated bool
}

func newLimitBuffer(limit int64) *limitBuffer {
	return &limitBuffer{limit: limit}
}

func (b *limitBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()

	n := len(p)
	remaining := b.limit - b.written
	if remaining <= 0 {
		b.truncated = b.truncated || n > 0
		return n, nil
	}
	if int64(len(p)) > remaining {
		p = p[:remaining]
		b.truncated = true
	}
	_, _ = b.buf.Write(p)
	b.written += int64(len(p))
	return n, nil
}

func (b *limitBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

func (b *limitBuffer) Truncated() bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.truncated
}
