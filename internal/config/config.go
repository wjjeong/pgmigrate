// Package config loads the source/target connection definitions and builds pgx configs.
// Passwords are never stored in the config file.
package config

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/jackc/pgx/v5"
	"gopkg.in/yaml.v3"
)

// Conn is one PostgreSQL endpoint.
type Conn struct {
	Name     string `yaml:"name"` // 화면 표시용. 비우면 "source"/"target"
	Host     string `yaml:"host"`
	Port     int    `yaml:"port"` // 0이면 5432
	Database string `yaml:"database"`
	User     string `yaml:"user"`
	SSLMode  string `yaml:"sslmode"` // 빈 값이면 "prefer"
	// AllowDrop은 타깃에서만 의미가 있다. true일 때만 DROP 후 재생성을 허용한다.
	AllowDrop bool `yaml:"allow_drop"`
}

// Config holds exactly one source and one target connection.
type Config struct {
	Source Conn `yaml:"source"`
	Target Conn `yaml:"target"`
}

const formatHint = "설정 파일은 source:와 target: 두 섹션으로 작성합니다 (pgmigrate.example.yaml 참고)"

// Load reads the YAML file at path and validates both connections.
// Unknown keys (e.g. the old connections:/env: format) are rejected.
func Load(path string) (Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return Config{}, fmt.Errorf("설정 파일을 읽을 수 없습니다 (%s): %w", path, err)
	}
	var top map[string]yaml.Node
	if err := yaml.Unmarshal(data, &top); err != nil {
		return Config{}, fmt.Errorf("설정 파일 형식이 올바르지 않습니다 (%s): %w", path, err)
	}
	if _, old := top["connections"]; old {
		return Config{}, fmt.Errorf("설정 파일이 예전 형식(connections:)입니다 (%s). %s", path, formatHint)
	}
	var cfg Config
	dec := yaml.NewDecoder(bytes.NewReader(data))
	dec.KnownFields(true)
	if err := dec.Decode(&cfg); err != nil && !errors.Is(err, io.EOF) {
		return Config{}, fmt.Errorf("설정 파일 형식이 올바르지 않습니다 (%s): %w. %s", path, err, formatHint)
	}
	for _, sec := range []struct {
		key  string
		c    *Conn
		node bool
	}{{"source", &cfg.Source, top["source"].Kind != 0}, {"target", &cfg.Target, top["target"].Kind != 0}} {
		if !sec.node {
			return Config{}, fmt.Errorf("설정 파일에 %s 섹션이 없습니다. %s", sec.key, formatHint)
		}
		c := sec.c
		for _, p := range []*string{&c.Name, &c.Host, &c.Database, &c.User, &c.SSLMode} {
			*p = strings.TrimSpace(*p)
		}
		for _, r := range []struct{ label, v string }{
			{"host", c.Host}, {"database", c.Database}, {"user", c.User},
		} {
			if r.v == "" {
				return Config{}, fmt.Errorf("%s: 필수 항목 %q 이(가) 비어 있습니다", sec.key, r.label)
			}
		}
		if c.Name == "" {
			c.Name = sec.key
		}
		if c.Port == 0 {
			c.Port = 5432
		}
		if c.SSLMode == "" {
			c.SSLMode = "prefer"
		}
	}
	cfg.Source.AllowDrop = false // 소스에서는 의미가 없으므로 무시한다
	return cfg, nil
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
