// pgmigrate: PostgreSQL 소규모 테이블 이관 TUI.
package main

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"pgmigrate/internal/app"
	"pgmigrate/internal/config"
	"pgmigrate/internal/migrate"
	"pgmigrate/internal/tui"
)

// version은 build.sh의 ldflags로 덮어쓴다.
var version = "dev"

func fileExists(p string) bool {
	st, err := os.Stat(p)
	return err == nil && !st.IsDir()
}

func exeDir() (string, error) {
	exe, err := os.Executable()
	if err != nil {
		return "", err
	}
	if r, err := filepath.EvalSymlinks(exe); err == nil {
		exe = r
	}
	return filepath.Dir(exe), nil
}

func main() { os.Exit(run()) }

func run() int {
	cfgFlag := flag.String("config", "", "설정 파일 경로 (기본: 실행 파일 옆 pgmigrate.yaml, 없으면 %APPDATA%\\pgmigrate\\pgmigrate.yaml)")
	showVersion := flag.Bool("version", false, "버전을 출력하고 종료")
	lockTimeout := flag.Duration("lock-timeout", 10*time.Second, "타깃 테이블 잠금 대기 시간")
	flag.Parse()

	if *showVersion {
		fmt.Println("pgmigrate", version)
		return 0
	}

	dir, err := exeDir()
	if err != nil {
		fmt.Fprintln(os.Stderr, "실행 파일 위치를 알 수 없습니다:", err)
		return 1
	}
	path := *cfgFlag
	if path == "" {
		path = config.ResolvePath(dir, os.Getenv("APPDATA"), fileExists)
	}
	if !fileExists(path) {
		fmt.Fprintf(os.Stderr, "설정 파일을 찾을 수 없습니다: %s\n", path)
		fmt.Fprintf(os.Stderr, "예시: %s 에 pgmigrate.example.yaml을 참고해 pgmigrate.yaml을 만드세요.\n", filepath.Join(dir, "pgmigrate.yaml"))
		return 2
	}
	conns, err := config.Load(path)
	if err != nil {
		fmt.Fprintln(os.Stderr, "설정 파일 오류:", err)
		return 2
	}

	b := app.NewPG(filepath.Join(dir, "logs"), migrate.Options{LockTimeout: *lockTimeout})
	defer b.Close()
	if _, err := tea.NewProgram(tui.New(b, conns), tea.WithAltScreen()).Run(); err != nil {
		fmt.Fprintln(os.Stderr, "실행 오류:", err)
		return 1
	}
	return 0
}
