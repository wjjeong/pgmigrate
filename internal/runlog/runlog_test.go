package runlog

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"
)

func TestOpen(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "logs") // 아직 없는 디렉터리
	now := time.Date(2006, 11, 2, 15, 4, 5, 0, time.Local)
	f, err := Open(dir, now)
	if err != nil {
		t.Fatal(err)
	}
	want := filepath.Join(dir, "pgmigrate-20061102-150405.log")
	if f.Path() != want {
		t.Errorf("Path() = %q, want %q", f.Path(), want)
	}
	f.Printf("안녕 %d", 1)
	f.Printf("두 줄\n둘째 줄\n")
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(want)
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimSuffix(string(data), "\n"), "\n")
	if len(lines) != 3 {
		t.Fatalf("lines = %q", lines)
	}
	ts := `\d{4}-\d{2}-\d{2}T\d{2}:\d{2}:\d{2}(Z|[+-]\d{2}:\d{2})`
	for i, body := range []string{"안녕 1", "두 줄", "둘째 줄"} {
		re := regexp.MustCompile("^" + ts + " " + regexp.QuoteMeta(body) + "$")
		if !re.MatchString(lines[i]) {
			t.Errorf("line %d = %q", i, lines[i])
		}
		stamp := strings.SplitN(lines[i], " ", 2)[0]
		if _, err := time.Parse(time.RFC3339, stamp); err != nil {
			t.Errorf("line %d stamp %q: %v", i, stamp, err)
		}
	}
}

func TestDiscard(t *testing.T) {
	Discard().Printf("무시 %s", "x") // panic 없이 동작해야 한다
}
