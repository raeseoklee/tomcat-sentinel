package process

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/raeseoklee/tomcat-sentinel/internal/command"
)

func TestProcInspectorReadsAliveProcess(t *testing.T) {
	root := t.TempDir()
	pid := 1234
	procDir := filepath.Join(root, "1234")
	if err := os.Mkdir(procDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(procDir, "stat"), []byte("1234 (java) S 1 2 3"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(procDir, "cmdline"), []byte("java\x00org.apache.catalina.startup.Bootstrap\x00"), 0o600); err != nil {
		t.Fatal(err)
	}

	info, err := ProcInspector{ProcRoot: root}.Inspect(pid)
	if err != nil {
		t.Fatal(err)
	}
	if !info.Alive || info.Zombie {
		t.Fatalf("info=%+v", info)
	}
}

func TestResolverChecksPIDFileAndCommandHint(t *testing.T) {
	root := t.TempDir()
	procDir := filepath.Join(root, "77")
	if err := os.Mkdir(procDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(procDir, "stat"), []byte("77 (java) S 1 2 3"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(procDir, "cmdline"), []byte("java\x00org.apache.catalina.startup.Bootstrap\x00"), 0o600); err != nil {
		t.Fatal(err)
	}
	pidFile := filepath.Join(t.TempDir(), "tomcat.pid")
	if err := os.WriteFile(pidFile, []byte("77\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	resolver := Resolver{
		PIDFile:            pidFile,
		ProcessCommandHint: "org.apache.catalina.startup.Bootstrap",
		Inspector:          ProcInspector{ProcRoot: root},
		Runner:             command.Runner{},
	}
	check, err := resolver.Check(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if !check.Alive || !check.Trusted || check.PID != 77 {
		t.Fatalf("check=%+v", check)
	}
}
