package logscan

import (
	"os"
	"path/filepath"
	"testing"
)

func TestScanClassifiesOOMFromTail(t *testing.T) {
	dir := t.TempDir()
	logPath := filepath.Join(dir, "catalina.out")
	content := "old line\njava.lang.OutOfMemoryError: Java heap space\n"
	if err := os.WriteFile(logPath, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}

	report, err := Scanner{
		Paths:            []string{logPath},
		TailBytes:        512,
		MaxFiles:         4,
		OOMPatterns:      []string{"java.lang.OutOfMemoryError"},
		ShutdownPatterns: []string{"Stopping service [Catalina]"},
	}.Scan()
	if err != nil {
		t.Fatal(err)
	}
	if report.Classification != ClassificationOOM {
		t.Fatalf("Classification=%q", report.Classification)
	}
	if len(report.Evidence) != 1 {
		t.Fatalf("Evidence=%+v", report.Evidence)
	}
}

func TestScanHonorsMaxFiles(t *testing.T) {
	dir := t.TempDir()
	for _, name := range []string{"a.log", "b.log", "c.log"} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte("SEVERE\n"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	report, err := Scanner{
		Paths:            []string{filepath.Join(dir, "*.log")},
		TailBytes:        512,
		MaxFiles:         2,
		ShutdownPatterns: []string{"SEVERE"},
	}.Scan()
	if err != nil {
		t.Fatal(err)
	}
	if report.FilesScanned != 2 {
		t.Fatalf("FilesScanned=%d", report.FilesScanned)
	}
}
