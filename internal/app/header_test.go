package app

import (
	"os"
	"strings"
	"testing"
	"time"

	"pgmigrate/internal/config"
	"pgmigrate/internal/plan"
	"pgmigrate/internal/runlog"
)

func TestWriteHeader_AllowDropOnTargetOnly(t *testing.T) {
	lf, err := runlog.Open(t.TempDir(), time.Now())
	if err != nil {
		t.Fatal(err)
	}
	writeHeader(lf, &plan.Plan{
		Source: config.Conn{Name: "dev", Host: "h1", Port: 5432, Database: "app", User: "u"},
		Target: config.Conn{Name: "qa", Host: "h2", Port: 5432, Database: "app_qa", User: "u", AllowDrop: true},
	})
	if err := lf.Close(); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(lf.Path())
	if err != nil {
		t.Fatal(err)
	}
	var src, tgt string
	for _, l := range strings.Split(string(raw), "\n") {
		switch {
		case strings.Contains(l, "소스: name=dev"):
			src = l
		case strings.Contains(l, "타깃: name=qa"):
			tgt = l
		}
	}
	if src == "" || tgt == "" {
		t.Fatalf("header lines missing:\n%s", raw)
	}
	if strings.Contains(src, "allow_drop") || !strings.Contains(tgt, "allow_drop=true") || strings.Contains(string(raw), "env=") {
		t.Fatalf("unexpected header:\n%s", raw)
	}
}
