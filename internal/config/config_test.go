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

func TestLoad(t *testing.T) {
	t.Run("defaults", func(t *testing.T) {
		p := writeYAML(t, "connections:\n  - {name: a, env: dev, host: h, database: d, user: u}\n")
		cs, err := Load(p)
		if err != nil {
			t.Fatal(err)
		}
		if len(cs) != 1 || cs[0].Port != 5432 || cs[0].SSLMode != "prefer" {
			t.Fatalf("unexpected: %+v", cs)
		}
	})
	t.Run("explicit values kept", func(t *testing.T) {
		p := writeYAML(t, "connections:\n  - {name: a, host: h, port: 6543, database: d, user: u, sslmode: require}\n")
		cs, err := Load(p)
		if err != nil {
			t.Fatal(err)
		}
		if cs[0].Port != 6543 || cs[0].SSLMode != "require" {
			t.Fatalf("unexpected: %+v", cs[0])
		}
	})
	t.Run("duplicate name", func(t *testing.T) {
		p := writeYAML(t, "connections:\n  - {name: a, host: h, database: d, user: u}\n  - {name: a, host: h2, database: d, user: u}\n")
		if _, err := Load(p); err == nil {
			t.Fatal("want error")
		}
	})
	for _, f := range []string{"name", "host", "database", "user"} {
		f := f
		t.Run("missing "+f, func(t *testing.T) {
			m := map[string]string{"name": "a", "host": "h", "database": "d", "user": "u"}
			delete(m, f)
			var parts []string
			for k, v := range m {
				parts = append(parts, k+": "+v)
			}
			p := writeYAML(t, "connections:\n  - {"+strings.Join(parts, ", ")+"}\n")
			if _, err := Load(p); err == nil {
				t.Fatal("want error")
			}
		})
	}
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

func TestIsPrd(t *testing.T) {
	if !(Conn{Env: "PRD"}).IsPrd() || (Conn{Env: "dev"}).IsPrd() {
		t.Fatal("IsPrd")
	}
}
