package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeYAML(t *testing.T, s string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "pgmigrate.yaml")
	if err := os.WriteFile(p, []byte(s), 0o600); err != nil {
		t.Fatal(err)
	}
	return p
}

const validYAML = `source:
  name: dev
  host: dev-db
  database: app
  user: migr
target:
  name: qa
  host: qa-db
  port: 6543
  database: app
  user: migr
  sslmode: require
  allow_drop: true
`

func TestLoad(t *testing.T) {
	t.Run("source and target", func(t *testing.T) {
		cfg, err := Load(writeYAML(t, validYAML))
		if err != nil {
			t.Fatal(err)
		}
		want := Config{
			Source: Conn{Name: "dev", Host: "dev-db", Port: 5432, Database: "app", User: "migr", SSLMode: "prefer"},
			Target: Conn{Name: "qa", Host: "qa-db", Port: 6543, Database: "app", User: "migr", SSLMode: "require", AllowDrop: true},
		}
		if cfg != want {
			t.Fatalf("got %+v\nwant %+v", cfg, want)
		}
	})
	t.Run("default names", func(t *testing.T) {
		cfg, err := Load(writeYAML(t, "source: {host: h, database: d, user: u}\ntarget: {host: h2, database: d, user: u}\n"))
		if err != nil {
			t.Fatal(err)
		}
		if cfg.Source.Name != "source" || cfg.Target.Name != "target" || cfg.Target.AllowDrop {
			t.Fatalf("unexpected: %+v", cfg)
		}
	})
	t.Run("allow_drop on source ignored", func(t *testing.T) {
		cfg, err := Load(writeYAML(t, "source: {host: h, database: d, user: u, allow_drop: true}\ntarget: {host: h2, database: d, user: u}\n"))
		if err != nil {
			t.Fatal(err)
		}
		if cfg.Source.AllowDrop {
			t.Fatal("source allow_drop must be ignored")
		}
	})
	for _, sec := range []string{"source", "target"} {
		sec := sec
		t.Run("missing section "+sec, func(t *testing.T) {
			y := "source: {host: h, database: d, user: u}\ntarget: {host: h2, database: d, user: u}\n"
			lines := strings.Split(y, "\n")
			if sec == "source" {
				y = lines[1] + "\n"
			} else {
				y = lines[0] + "\n"
			}
			_, err := Load(writeYAML(t, y))
			if err == nil || !strings.Contains(err.Error(), sec) {
				t.Fatalf("want error naming %s: %v", sec, err)
			}
		})
		for _, f := range []string{"host", "database", "user"} {
			f := f
			t.Run(sec+" missing "+f, func(t *testing.T) {
				m := map[string]string{"host": "h", "database": "d", "user": "u"}
				m[f] = "'  '"
				var parts []string
				for k, v := range m {
					parts = append(parts, k+": "+v)
				}
				body := "{" + strings.Join(parts, ", ") + "}"
				ok := "{host: h9, database: d, user: u}"
				y := "source: " + ok + "\ntarget: " + body + "\n"
				if sec == "source" {
					y = "source: " + body + "\ntarget: " + ok + "\n"
				}
				_, err := Load(writeYAML(t, y))
				if err == nil || !strings.Contains(err.Error(), sec) || !strings.Contains(err.Error(), f) {
					t.Fatalf("want error naming %s/%s: %v", sec, f, err)
				}
			})
		}
	}
	t.Run("old connections format", func(t *testing.T) {
		_, err := Load(writeYAML(t, "connections:\n  - {name: a, env: dev, host: h, database: d, user: u}\n"))
		if err == nil || !strings.Contains(err.Error(), "source") || !strings.Contains(err.Error(), "target") {
			t.Fatalf("want error suggesting source/target: %v", err)
		}
	})
	t.Run("unknown field env", func(t *testing.T) {
		_, err := Load(writeYAML(t, "source: {host: h, database: d, user: u, env: dev}\ntarget: {host: h2, database: d, user: u}\n"))
		if err == nil {
			t.Fatal("want error for unknown field")
		}
	})
	t.Run("fields trimmed", func(t *testing.T) {
		cfg, err := Load(writeYAML(t, "source: {name: ' a ', host: ' h ', database: ' d ', user: ' u ', sslmode: ' disable '}\ntarget: {host: h2, database: d, user: u}\n"))
		if err != nil {
			t.Fatal(err)
		}
		if c := cfg.Source; c.Name != "a" || c.Host != "h" || c.Database != "d" || c.User != "u" || c.SSLMode != "disable" {
			t.Fatalf("not trimmed: %+v", c)
		}
	})
	t.Run("empty file", func(t *testing.T) {
		if _, err := Load(writeYAML(t, "")); err == nil {
			t.Fatal("want error")
		}
	})
	t.Run("missing file", func(t *testing.T) {
		if _, err := Load(filepath.Join(t.TempDir(), "nope.yaml")); err == nil {
			t.Fatal("want error")
		}
	})
}

func TestResolvePath(t *testing.T) {
	exe := filepath.Join("x", "exe")
	app := filepath.Join("x", "app")
	exePath := filepath.Join(exe, "pgmigrate.yaml")
	appPath := filepath.Join(app, "pgmigrate", "pgmigrate.yaml")
	if got := ResolvePath(exe, app, func(p string) bool { return p == exePath }); got != exePath {
		t.Fatalf("got %q", got)
	}
	if got := ResolvePath(exe, app, func(string) bool { return false }); got != appPath {
		t.Fatalf("got %q", got)
	}
}

func TestPgxConfig(t *testing.T) {
	c := Conn{Name: "a", Host: "h", Port: 5432, Database: "d", User: "u", SSLMode: "prefer"}
	t.Setenv("PGPASSWORD", "envpw")
	cfg, err := c.PgxConfig("")
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Password != "envpw" {
		t.Fatalf("got %q", cfg.Password)
	}
	cfg, err = c.PgxConfig("pw")
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Password != "pw" {
		t.Fatalf("got %q", cfg.Password)
	}
	for k, want := range map[string]string{
		"search_path": "pg_catalog", "DateStyle": "ISO", "IntervalStyle": "postgres", "client_encoding": "UTF8",
	} {
		if got := cfg.RuntimeParams[k]; got != want {
			t.Errorf("RuntimeParams[%s] = %q, want %q", k, got, want)
		}
	}
	weird := c
	weird.Host = `my host'x\y`
	weird.Database = `d'b`
	cfg, err = weird.PgxConfig("pw")
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Host != `my host'x\y` || cfg.Database != `d'b` || cfg.User != "u" {
		t.Fatalf("got %+v", cfg.Config)
	}
}

func TestSameDatabase(t *testing.T) {
	a := Conn{Host: "Host", Port: 5432, Database: "d"}
	if !SameDatabase(a, Conn{Host: "Host", Port: 5432, Database: "d", User: "other"}) {
		t.Fatal("want true")
	}
	if SameDatabase(a, Conn{Host: "host", Port: 5432, Database: "d"}) {
		t.Fatal("case differs")
	}
	if SameDatabase(Conn{Host: "localhost", Port: 5432, Database: "d"}, Conn{Host: "127.0.0.1", Port: 5432, Database: "d"}) {
		t.Fatal("localhost vs ip")
	}
	if SameDatabase(a, Conn{Host: "Host", Port: 5433, Database: "d"}) {
		t.Fatal("port")
	}
	if SameDatabase(a, Conn{Host: "Host", Port: 5432, Database: "e"}) {
		t.Fatal("db")
	}
}
