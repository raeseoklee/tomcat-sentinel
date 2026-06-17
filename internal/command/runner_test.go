package command

import (
	"context"
	"strings"
	"testing"
	"time"
)

func TestRunnerCapturesBoundedOutput(t *testing.T) {
	runner := Runner{Timeout: time.Second, OutputMaxBytes: 5}
	result, err := runner.Run(context.Background(), "printf abcdefgh")
	if err != nil {
		t.Fatal(err)
	}
	if result.Stdout != "abcde" {
		t.Fatalf("stdout=%q", result.Stdout)
	}
	if !result.StdoutTruncated {
		t.Fatal("expected stdout truncation")
	}
}

func TestRunnerReportsExitError(t *testing.T) {
	runner := Runner{Timeout: time.Second, OutputMaxBytes: 64}
	result, err := runner.Run(context.Background(), "echo nope >&2; exit 7")
	if err == nil {
		t.Fatal("expected error")
	}
	if result.ExitCode != 7 {
		t.Fatalf("ExitCode=%d err=%v stderr=%q", result.ExitCode, err, result.Stderr)
	}
	if !strings.Contains(result.Stderr, "nope") {
		t.Fatalf("stderr=%q", result.Stderr)
	}
}
