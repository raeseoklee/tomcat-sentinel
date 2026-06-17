package backup

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/raeseoklee/tomcat-sentinel/internal/logscan"
)

type Manager struct {
	LogPaths        []string
	Dir             string
	MaxBytesPerFile int64
	CopyBufferBytes int
	RetentionDays   int
	Version         string
}

type Incident struct {
	Time           time.Time
	Classification string
	PID            int
	Reason         string
	Evidence       []logscan.Evidence
}

type Result struct {
	Dir      string
	Manifest string
	Files    []FileResult
}

type FileResult struct {
	Source string `json:"source"`
	Target string `json:"target"`
	Bytes  int64  `json:"bytes"`
}

type Manifest struct {
	Time           string             `json:"time"`
	Classification string             `json:"classification"`
	PID            int                `json:"pid,omitempty"`
	Reason         string             `json:"reason"`
	Evidence       []logscan.Evidence `json:"evidence,omitempty"`
	Files          []FileResult       `json:"files"`
	Version        string             `json:"version"`
}

func (m Manager) Backup(incident Incident) (Result, error) {
	if m.Dir == "" {
		return Result{}, fmt.Errorf("backup dir is empty")
	}
	if m.MaxBytesPerFile <= 0 {
		m.MaxBytesPerFile = 8 * 1024 * 1024
	}
	if m.CopyBufferBytes <= 0 {
		m.CopyBufferBytes = 32 * 1024
	}

	files, err := expandPaths(m.LogPaths)
	if err != nil {
		return Result{}, err
	}
	incidentDir := filepath.Join(m.Dir, incidentDirName(incident))
	if err := os.MkdirAll(incidentDir, 0o750); err != nil {
		return Result{}, fmt.Errorf("create backup dir: %w", err)
	}

	result := Result{Dir: incidentDir}
	usedNames := map[string]int{}
	for _, source := range files {
		target := filepath.Join(incidentDir, uniqueName(filepath.Base(source), usedNames))
		bytesCopied, err := copyTail(source, target, m.MaxBytesPerFile, m.CopyBufferBytes)
		if err != nil {
			return result, fmt.Errorf("backup %s: %w", source, err)
		}
		result.Files = append(result.Files, FileResult{
			Source: source,
			Target: target,
			Bytes:  bytesCopied,
		})
	}

	manifestPath := filepath.Join(incidentDir, "manifest.json")
	manifest := Manifest{
		Time:           incident.Time.UTC().Format(time.RFC3339),
		Classification: incident.Classification,
		PID:            incident.PID,
		Reason:         incident.Reason,
		Evidence:       incident.Evidence,
		Files:          result.Files,
		Version:        m.Version,
	}
	if err := writeJSON(manifestPath, manifest); err != nil {
		return result, err
	}
	result.Manifest = manifestPath

	if m.RetentionDays > 0 {
		_ = ApplyRetention(m.Dir, time.Duration(m.RetentionDays)*24*time.Hour, incident.Time)
	}
	return result, nil
}

func copyTail(source, target string, maxBytes int64, bufferBytes int) (int64, error) {
	src, err := os.Open(source)
	if err != nil {
		return 0, err
	}
	defer src.Close()

	info, err := src.Stat()
	if err != nil {
		return 0, err
	}
	start := int64(0)
	if info.Size() > maxBytes {
		start = info.Size() - maxBytes
	}
	if _, err := src.Seek(start, io.SeekStart); err != nil {
		return 0, err
	}

	dst, err := os.OpenFile(target, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o640)
	if err != nil {
		return 0, err
	}
	defer dst.Close()

	limit := info.Size() - start
	buf := make([]byte, bufferBytes)
	return io.CopyBuffer(dst, io.LimitReader(src, limit), buf)
}

func writeJSON(path string, value any) error {
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o640)
	if err != nil {
		return err
	}
	defer f.Close()
	enc := json.NewEncoder(f)
	enc.SetIndent("", "  ")
	return enc.Encode(value)
}

func ApplyRetention(root string, maxAge time.Duration, now time.Time) error {
	entries, err := os.ReadDir(root)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	cutoff := now.Add(-maxAge)
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		path := filepath.Join(root, entry.Name())
		info, err := entry.Info()
		if err != nil {
			continue
		}
		if info.ModTime().Before(cutoff) {
			_ = os.RemoveAll(path)
		}
	}
	return nil
}

func expandPaths(patterns []string) ([]string, error) {
	seen := map[string]struct{}{}
	var files []string
	for _, pattern := range patterns {
		matches, err := filepath.Glob(pattern)
		if err != nil {
			return nil, fmt.Errorf("invalid log path pattern %q: %w", pattern, err)
		}
		if len(matches) == 0 && !strings.ContainsAny(pattern, "*?[") {
			matches = []string{pattern}
		}
		sort.Strings(matches)
		for _, match := range matches {
			info, err := os.Stat(match)
			if err != nil || info.IsDir() {
				continue
			}
			if _, ok := seen[match]; ok {
				continue
			}
			seen[match] = struct{}{}
			files = append(files, match)
		}
	}
	sort.Strings(files)
	return files, nil
}

func incidentDirName(incident Incident) string {
	ts := incident.Time.UTC().Format("20060102T150405Z")
	classification := sanitize(incident.Classification)
	if classification == "" {
		classification = "unknown"
	}
	if incident.PID > 0 {
		return fmt.Sprintf("%s-%s-pid-%d", ts, classification, incident.PID)
	}
	return fmt.Sprintf("%s-%s", ts, classification)
}

func uniqueName(base string, used map[string]int) string {
	if base == "" || base == "." || base == string(filepath.Separator) {
		base = "log"
	}
	base = sanitize(base)
	count := used[base]
	used[base] = count + 1
	if count == 0 {
		return base
	}
	ext := filepath.Ext(base)
	stem := strings.TrimSuffix(base, ext)
	return fmt.Sprintf("%s-%d%s", stem, count+1, ext)
}

func sanitize(s string) string {
	s = strings.TrimSpace(s)
	s = strings.Map(func(r rune) rune {
		switch {
		case r >= 'a' && r <= 'z':
			return r
		case r >= 'A' && r <= 'Z':
			return r
		case r >= '0' && r <= '9':
			return r
		case r == '.', r == '-', r == '_':
			return r
		default:
			return '-'
		}
	}, s)
	return strings.Trim(s, "-")
}
