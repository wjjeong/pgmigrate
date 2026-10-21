// Package config loads connection definitions and builds pgx configs.
// Passwords are never stored in the config file.
package config

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/jackc/pgx/v5"
	"gopkg.in/yaml.v3"
)

type Conn struct {
	Name     string `yaml:"name"`
	Env      string `yaml:"env"` // "dev" | "qa" | "prd" | 자유 문자열
	Host     string `yaml:"host"`
	Port     int    `yaml:"port"` // 0이면 5432
	Database string `yaml:"database"`
	User     string `yaml:"user"`
	SSLMode  string `yaml:"sslmode"` // 빈 값이면 "prefer"
	// AllowDrop은 타깃에서만 의미가 있다. true일 때만 DROP 후 재생성을 허용한다.
	AllowDrop bool `yaml:"allow_drop"`
}

type file struct {
	Connections []Conn `yaml:"connections"`
}

// IsPrd reports whether c is a production connection. prd, prod and
// production are accepted (case-insensitive, surrounding spaces ignored) so a
// different spelling never disables the production safeguards.
func (c Conn) IsPrd() bool {
	switch strings.ToLower(strings.TrimSpace(c.Env)) {
	case "prd", "prod", "production":
		return true
	}
	return false
}

// Load reads the YAML file at path and validates the connections.
func Load(path string) ([]Conn, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("설정 파일을 읽을 수 없습니다 (%s): %w", path, err)
	}
	var f file
	if err := yaml.Unmarshal(data, &f); err != nil {
		return nil, fmt.Errorf("설정 파일 형식이 올바르지 않습니다 (%s): %w", path, err)
	}
	seen := map[string]bool{}
	for i := range f.Connections {
		c := &f.Connections[i]
		for _, p := range []*string{&c.Name, &c.Env, &c.Host, &c.Database, &c.User} {
			*p = strings.TrimSpace(*p)
		}
		for _, r := range []struct{ label, v string }{
			{"name", c.Name}, {"host", c.Host}, {"database", c.Database}, {"user", c.User},
		} {
			if r.v == "" {
				return nil, fmt.Errorf("연결 #%d: 필수 항목 %q 이(가) 비어 있습니다", i+1, r.label)
			}
		}
		if seen[c.Name] {
			return nil, fmt.Errorf("연결 이름이 중복되었습니다: %q", c.Name)
		}
		seen[c.Name] = true
		if c.Port == 0 {
			c.Port = 5432
		}
		if c.SSLMode == "" {
			c.SSLMode = "prefer"
		}
	}
	return f.Connections, nil
}

// ResolvePath prefers exeDir/pgmigrate.yaml, else appData/pgmigrate/pgmigrate.yaml.
func ResolvePath(exeDir, appData string, exists func(string) bool) string {
	p := filepath.Join(exeDir, "pgmigrate.yaml")
	if exists(p) {
		return p
	}
	return filepath.Join(appData, "pgmigrate", "pgmigrate.yaml")
}

func quote(s string) string {
	s = strings.ReplaceAll(s, `\`, `\\`)
	s = strings.ReplaceAll(s, `'`, `\'`)
	return "'" + s + "'"
}

// PgxConfig builds a pgx config. A non-empty password overrides the one pgx
// derives from PGPASSWORD/pgpass.
func (c Conn) PgxConfig(password string) (*pgx.ConnConfig, error) {
	port := c.Port
	if port == 0 {
		port = 5432
	}
	ssl := c.SSLMode
	if ssl == "" {
		ssl = "prefer"
	}
	dsn := fmt.Sprintf("host=%s port=%d dbname=%s user=%s sslmode=%s",
		quote(c.Host), port, quote(c.Database), quote(c.User), quote(ssl))
	cfg, err := pgx.ParseConfig(dsn)
	if err != nil {
		return nil, fmt.Errorf("연결 설정이 올바르지 않습니다 (%s): %w", c.Name, err)
	}
	if password != "" {
		cfg.Password = password
	}
	// 카탈로그 함수(pg_get_expr, format_type 등)의 이름 표기와 COPY 텍스트 형식이
	// 서버/역할 설정에 따라 달라지지 않도록 세션 설정을 고정한다. search_path가
	// pg_catalog뿐이므로 사용자 객체는 항상 스키마까지 붙어서 출력된다.
	for k, v := range map[string]string{
		"search_path":     "pg_catalog",
		"DateStyle":       "ISO",
		"IntervalStyle":   "postgres",
		"client_encoding": "UTF8",
	} {
		cfg.RuntimeParams[k] = v
	}
	return cfg, nil
}

// SameDatabase compares host, port and database as plain strings.
func SameDatabase(a, b Conn) bool {
	return a.Host == b.Host && a.Port == b.Port && a.Database == b.Database
}
