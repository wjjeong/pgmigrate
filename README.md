# pgmigrate

PostgreSQL 18.4 데이터베이스 사이에서 소규모 테이블을 옮기는 터미널(TUI) 도구입니다.
`pg_dump`/`psql`/libpq 없이 exe 하나로 동작합니다.

## 설치

`dist/pgmigrate.exe` 한 파일을 원하는 폴더에 복사하면 끝입니다. 빌드는 다음과 같이 합니다.

```sh
./build.sh   # dist/pgmigrate.exe (windows/amd64), dist/pgmigrate-darwin
```

플래그:

| 플래그 | 설명 |
| --- | --- |
| `-config <경로>` | 설정 파일 경로 (생략 시 아래 순서로 찾음) |
| `-lock-timeout <시간>` | 타깃 테이블 잠금 대기 시간 (기본 `10s`, 예: `30s`) |
| `-version` | 버전 출력 후 종료 |

## 설정 파일

다음 순서로 찾습니다. 둘 다 없으면 안내 메시지를 출력하고 종료 코드 2로 끝납니다.

1. exe와 같은 폴더의 `pgmigrate.yaml`
2. `%APPDATA%\pgmigrate\pgmigrate.yaml`

예시 (`pgmigrate.example.yaml` 참고):

```yaml
connections:
  - name: local-dev
    env: dev          # dev / qa / prd 등. prd는 추가 안전 장치가 적용됩니다
    host: localhost
    port: 5432
    database: appdb
    user: app
    sslmode: prefer
  - name: prd-server
    env: prd
    host: prd-db.example.com
    port: 5432
    database: appdb
    user: app_readonly
    sslmode: require
```

## 비밀번호

설정 파일에는 비밀번호를 쓰지 않으며, 로그에도 기록하지 않습니다.

- 연결 화면에서 입력합니다.
- 비워 두면 libpq와 같은 방식으로 `PGPASSWORD` 환경 변수, 그다음 `%APPDATA%\postgresql\pgpass.conf`를 사용합니다.

## 모드와 안전 장치

테이블마다 모드를 고릅니다 (`c`/`t`/`a`).

- `create`: 타깃에 테이블이 없을 때 DDL(시퀀스, 제약조건, 인덱스 등)을 만들고 데이터를 복사합니다. `d`로 DropRecreate(기존 테이블 삭제 후 재생성)를 켤 수 있습니다.
- `truncate`: 타깃의 기존 데이터를 `TRUNCATE`한 뒤 복사합니다.
- `append`: 기존 데이터를 두고 행을 추가합니다.

안전 장치:

- 모든 작업은 타깃의 단일 트랜잭션입니다. 하나라도 실패하거나 취소(Ctrl+C)하면 전체 롤백됩니다.
- 실행 전에 미리보기에서 실행 순서, 행 수, 검사 결과(경고/오류), DDL(`v`)을 확인합니다. 오류가 있으면 진행할 수 없습니다.
- 복사 후 행 수를 검증하고, 맞지 않으면 롤백합니다.
- 소스와 같은 데이터베이스는 타깃으로 고를 수 없습니다.
- 타깃이 `prd`이면 실행 전에 데이터베이스 이름을 정확히 입력해야 하며, DropRecreate는 사용할 수 없습니다.
- 소스는 읽기 전용 스냅샷 트랜잭션으로 읽고, `CASCADE`는 어디에서도 쓰지 않습니다.

## 로그

실행마다 `<exe 폴더>/logs/pgmigrate-YYYYMMDD-HHMMSS.log`에 실행한 SQL과 행 수 검증 결과가 남습니다. 결과 화면에 경로가 표시됩니다.

## 통합 테스트

Docker로 테스트용 DB 두 개(소스 `:55432`, 타깃 `:55433`, 계정 `postgres`/`postgres`)를 띄웁니다.

```sh
docker compose -f docker-compose.test.yml up -d
GOTOOLCHAIN=local go test -tags integration ./...
```

`go test ./...`는 DB 없이 통과합니다.
