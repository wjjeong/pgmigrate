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

소스 하나와 타깃 하나만 적습니다. `name`, `port`, `sslmode`는 생략할 수 있습니다 (기본값 `source`/`target`, `5432`, `prefer`).

```yaml
source:
  name: dev
  host: localhost
  port: 5432
  database: appdb
  user: app
  sslmode: prefer
target:
  name: qa
  host: qa-db.example.com
  port: 5432
  database: appdb
  user: app
  sslmode: require
  allow_drop: false   # true일 때만 DROP 후 재생성을 쓸 수 있습니다 (타깃에서만 의미가 있음)
```

예전 `connections:` 목록 형식이나 `env:` 같은 알 수 없는 항목은 오류로 거부됩니다.

## 비밀번호

설정 파일에는 비밀번호를 쓰지 않으며, 로그에도 기록하지 않습니다.

- 연결 화면에서 소스/타깃 비밀번호를 입력합니다.
- 비워 두면 libpq와 같은 방식으로 `PGPASSWORD` 환경 변수, 그다음 `%APPDATA%\postgresql\pgpass.conf`를 사용합니다.

## 모드와 안전 장치

테이블마다 모드를 고릅니다 (`c`/`t`/`a`).

- `create`: 타깃에 테이블이 없을 때 DDL(시퀀스, 제약조건, 인덱스 등)을 만들고 데이터를 복사합니다. `d`로 DropRecreate(기존 테이블 삭제 후 재생성)를 켤 수 있습니다.
- `truncate`: 타깃의 기존 데이터를 `TRUNCATE`한 뒤 복사합니다.
- `append`: 기존 데이터를 두고 행을 추가합니다.

안전 장치:

- 모든 작업은 타깃의 단일 트랜잭션입니다. 하나라도 실패하거나 취소(Ctrl+C)하면 전체 롤백됩니다. 커밋 단계에 들어간 뒤에는 취소할 수 없습니다.
- 커밋 중 연결이 끊기는 등으로 커밋 여부를 알 수 없으면 결과 화면과 로그에 "커밋 여부 불명확"으로 표시됩니다. 이때는 다시 실행하기 전에 타깃 DB를 직접 확인하세요 (특히 append).
- 실행 전에 미리보기에서 실행 순서, 행 수, 검사 결과(경고/오류), DDL(`v`)을 확인합니다. 오류가 있으면 진행할 수 없습니다.
- 복사 후 행 수를 검증하고, 맞지 않으면 롤백합니다.
- 소스와 타깃이 같은 데이터베이스이면 연결하지 않습니다. 연결 후에도 서버 식별자(system_identifier), 데이터베이스 이름, 서버 시작 시각을 비교하므로 `localhost`/`127.0.0.1`처럼 호스트를 달리 써도 막힙니다.
- 타깃은 항상 보호됩니다. 실행 전 확인 화면에서 타깃 데이터베이스 이름을 정확히(대소문자 구분) 입력해야 실행되며, 삭제될 테이블(TRUNCATE / DROP 후 재생성)이 나열됩니다.
- DropRecreate(DROP 후 재생성)는 타깃 설정에 `allow_drop: true`가 있을 때만 켤 수 있습니다. 없으면 `d` 키가 거부되고, 검사와 실행 단계에서도 다시 막힙니다.
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
