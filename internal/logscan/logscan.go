package logscan

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

const (
	ClassificationOOM      = "oom"
	ClassificationShutdown = "shutdown"
	ClassificationCrash    = "crash"
)

type Scanner struct {
	Paths            []string
	TailBytes        int64
	MaxFiles         int
	OOMPatterns      []string
	ShutdownPatterns []string
}

type Report struct {
	Classification string
	Evidence       []Evidence
	FilesScanned   int
}

type Evidence struct {
	File     string `json:"file"`
	Category string `json:"category"`
	Pattern  string `json:"pattern"`
	Excerpt  string `json:"excerpt"`
}

func (s Scanner) Scan() (Report, error) {
	report := Report{Classification: ClassificationCrash}
	files, err := expandPaths(s.Paths)
	if err != nil {
		return report, err
	}
	if s.MaxFiles > 0 && len(files) > s.MaxFiles {
		files = files[:s.MaxFiles]
	}
	tailBytes := s.TailBytes
	if tailBytes <= 0 {
		tailBytes = 512 * 1024
	}

	for _, file := range files {
		evidence, err := scanFile(file, tailBytes, s.OOMPatterns, s.ShutdownPatterns)
		if err != nil {
			if os.IsNotExist(err) {
				continue
			}
			return report, err
		}
		report.FilesScanned++
		report.Evidence = append(report.Evidence, evidence...)
	}
	report.Classification = classify(report.Evidence)
	return report, nil
}

func expandPaths(patterns []string) ([]string, error) {
	seen := map[string]struct{}{}
	var files []string
	for _, pattern := range patterns {
		pattern = strings.TrimSpace(pattern)
		if pattern == "" {
			continue
		}
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

func scanFile(path string, tailBytes int64, oomPatterns, shutdownPatterns []string) ([]Evidence, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	info, err := f.Stat()
	if err != nil {
		return nil, err
	}
	start := int64(0)
	if info.Size() > tailBytes {
		start = info.Size() - tailBytes
	}
	if _, err := f.Seek(start, io.SeekStart); err != nil {
		return nil, err
	}

	maxPattern := maxPatternLength(oomPatterns, shutdownPatterns)
	if maxPattern < 1 {
		maxPattern = 1
	}
	overlapLimit := maxPattern - 1
	if overlapLimit > 4096 {
		overlapLimit = 4096
	}

	buf := make([]byte, 32*1024)
	overlap := ""
	var evidence []Evidence
	for {
		n, readErr := f.Read(buf)
		if n > 0 {
			chunk := overlap + string(buf[:n])
			evidence = append(evidence, findEvidence(path, chunk, oomPatterns, shutdownPatterns)...)
			if len(chunk) > overlapLimit {
				overlap = chunk[len(chunk)-overlapLimit:]
			} else {
				overlap = chunk
			}
			if len(evidence) >= 20 {
				evidence = evidence[:20]
				break
			}
		}
		if readErr == io.EOF {
			break
		}
		if readErr != nil {
			return evidence, readErr
		}
	}
	return dedupeEvidence(evidence), nil
}

func findEvidence(file, chunk string, oomPatterns, shutdownPatterns []string) []Evidence {
	var evidence []Evidence
	for _, pattern := range oomPatterns {
		if idx := strings.Index(chunk, pattern); idx >= 0 {
			evidence = append(evidence, Evidence{
				File:     file,
				Category: ClassificationOOM,
				Pattern:  pattern,
				Excerpt:  excerpt(chunk, idx, len(pattern)),
			})
		}
	}
	for _, pattern := range shutdownPatterns {
		if idx := strings.Index(chunk, pattern); idx >= 0 {
			evidence = append(evidence, Evidence{
				File:     file,
				Category: ClassificationShutdown,
				Pattern:  pattern,
				Excerpt:  excerpt(chunk, idx, len(pattern)),
			})
		}
	}
	return evidence
}

func classify(evidence []Evidence) string {
	classification := ClassificationCrash
	for _, item := range evidence {
		if item.Category == ClassificationOOM {
			return ClassificationOOM
		}
		if item.Category == ClassificationShutdown {
			classification = ClassificationShutdown
		}
	}
	return classification
}

func excerpt(s string, idx, patternLen int) string {
	start := idx - 120
	if start < 0 {
		start = 0
	}
	end := idx + patternLen + 120
	if end > len(s) {
		end = len(s)
	}
	out := s[start:end]
	out = strings.ReplaceAll(out, "\x00", " ")
	out = strings.ReplaceAll(out, "\r", " ")
	out = strings.ReplaceAll(out, "\n", " ")
	if len(out) > 256 {
		out = out[:256]
	}
	return strings.TrimSpace(out)
}

func maxPatternLength(groups ...[]string) int {
	maxLen := 0
	for _, group := range groups {
		for _, pattern := range group {
			if len(pattern) > maxLen {
				maxLen = len(pattern)
			}
		}
	}
	return maxLen
}

func dedupeEvidence(evidence []Evidence) []Evidence {
	seen := map[string]struct{}{}
	out := make([]Evidence, 0, len(evidence))
	for _, item := range evidence {
		key := item.File + "\x00" + item.Category + "\x00" + item.Pattern
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		out = append(out, item)
	}
	return out
}
