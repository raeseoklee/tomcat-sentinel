package backup

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/raeseoklee/jvm-sentinel/internal/logscan"
)

func TestBackupCopiesTailAndManifest(t *testing.T) {
	dir := t.TempDir()
	logPath := filepath.Join(dir, "catalina.out")
	if err := os.WriteFile(logPath, []byte("0123456789abcdef"), 0o600); err != nil {
		t.Fatal(err)
	}
	backupDir := filepath.Join(dir, "backup")

	result, err := Manager{
		LogPaths:        []string{logPath},
		Dir:             backupDir,
		MaxBytesPerFile: 4,
		CopyBufferBytes: 2,
		RetentionDays:   7,
		Version:         "test",
	}.Backup(Incident{
		Time:           time.Date(2026, 6, 17, 1, 2, 3, 0, time.UTC),
		Classification: logscan.ClassificationOOM,
		PID:            99,
		Reason:         "pid.file",
		Evidence:       []logscan.Evidence{{Category: "oom", Pattern: "oom"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Files) != 1 || result.Files[0].Bytes != 4 {
		t.Fatalf("result=%+v", result)
	}
	copied, err := os.ReadFile(result.Files[0].Target)
	if err != nil {
		t.Fatal(err)
	}
	if string(copied) != "cdef" {
		t.Fatalf("copied=%q", copied)
	}
	manifestData, err := os.ReadFile(result.Manifest)
	if err != nil {
		t.Fatal(err)
	}
	var manifest Manifest
	if err := json.Unmarshal(manifestData, &manifest); err != nil {
		t.Fatal(err)
	}
	if manifest.Classification != logscan.ClassificationOOM || manifest.PID != 99 {
		t.Fatalf("manifest=%+v", manifest)
	}
}
