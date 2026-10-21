// Package runlog writes the per-run execution log file.
package runlog

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// Logger receives log lines.
type Logger interface {
	Printf(format string, args ...any)
}

// File is a log file; every line is prefixed with an RFC3339 timestamp.
type File struct {
	mu   sync.Mutex
	f    *os.File
	path string
}

// Open creates dir if needed and opens dir/pgmigrate-YYYYMMDD-HHMMSS.log.
func Open(dir string, now time.Time) (*File, error) {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, fmt.Errorf("로그 디렉터리를 만들 수 없습니다 (%s): %w", dir, err)
	}
	path := filepath.Join(dir, "pgmigrate-"+now.Format("20060102-150405")+".log")
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return nil, fmt.Errorf("로그 파일을 열 수 없습니다 (%s): %w", path, err)
	}
	return &File{f: f, path: path}, nil
}

// Printf writes the formatted message; each of its lines gets a timestamp.
// Write errors are ignored: logging must never stop a migration.
func (f *File) Printf(format string, args ...any) {
	msg := strings.TrimSuffix(fmt.Sprintf(format, args...), "\n")
	stamp := time.Now().Format(time.RFC3339)
	var b strings.Builder
	for _, line := range strings.Split(msg, "\n") {
		b.WriteString(stamp)
		b.WriteString(" ")
		b.WriteString(line)
		b.WriteString("\n")
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	_, _ = f.f.WriteString(b.String())
}

// Path returns the log file path.
func (f *File) Path() string { return f.path }

// Close closes the file.
func (f *File) Close() error {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.f.Close()
}

type discard struct{}

func (discard) Printf(string, ...any) {}

// Discard returns a Logger that drops everything.
func Discard() Logger { return discard{} }
