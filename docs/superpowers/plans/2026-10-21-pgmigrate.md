# pgmigrate Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** dev/qa/prd PostgreSQL 18.4 DB 사이에서 작은 테이블을 create / truncate / append 모드로 옮기는 Windows용 단일 exe TUI 도구를 만든다.

**Architecture:** 카탈로그 조회(`catalog`) → DDL 생성(`ddl`) → 사실 수집 + 규칙 검사로 실행 계획 생성(`plan`) → 단일 타깃 트랜잭션으로 실행(`migrate`) → Bubble Tea 화면(`tui`)이 `app.Backend` 인터페이스를 통해 위 기능을 호출한다. 규칙 검사(`plan.Check`)와 DDL 생성은 DB 없이 단위 테스트하고, 카탈로그·실행은 Docker의 PG 18.4 두 대로 통합 테스트한다.

**Tech Stack:** Go 1.23, pgx v5, Bubble Tea v1 / Bubbles / Lip Gloss, gopkg.in/yaml.v3, Docker(postgres:18.4)

**Spec:** `docs/superpowers/specs/2026-10-21-pgmigrate-design.md`

## Global Constraints

- 모듈 경로 `pgmigrate`, `go 1.23` (로컬 툴체인 go1.23.2). `GOTOOLCHAIN=local`로 빌드하고, 의존성은 go1.23에서 빌드되는 버전으로 고정한다.
- `CGO_ENABLED=0`. 배포물은 `GOOS=windows GOARCH=amd64`로 만든 `pgmigrate.exe` 하나다.
- 소스/타깃은 PostgreSQL 18.4만 대상으로 한다. 다른 버전 대응 코드는 쓰지 않는다.
- pg_dump / pg_restore / libpq / psql 의존 금지.
- SQL에 들어가는 모든 식별자는 `pgx.Identifier{...}.Sanitize()`로 인용한다. 문자열 값은 바인드 파라미터로 넘긴다.
- `CASCADE`는 어디에서도 쓰지 않는다.
- 비밀번호는 파일이나 로그에 쓰지 않는다.
- 화면 문구와 오류 메시지는 한국어로 쓴다.
- 통합 테스트는 build tag `integration`으로 분리한다. `go test ./...`는 DB 없이 통과해야 한다.
- 커밋 메시지 끝에 `Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>`를 붙인다.

## Review Focus

1. **실행 중 소스 오류·연결 끊김**: COPY 도중 소스 쪽이 실패하면 타깃은 롤백되어야 하고, 파이프 고루틴은 데드락 없이 끝나야 한다. → Task 7 `TestExecute_SourceFailureRollsBack`
2. **실행 중 Ctrl+C / 컨텍스트 취소**: 일부만 커밋되는 일 없이 전체가 롤백되어야 한다. → Task 7 `TestExecute_CancelRollsBack`, Task 9 `TestRunScreen_CtrlCCancels`
3. **특수 식별자**: 대소문자 섞인 이름, 한글, 예약어(`"Order"`, `공통코드`, `"user"`)가 들어가도 모든 생성 SQL이 동작해야 한다. → Task 3·Task 7 픽스처에 포함
4. **데이터 왕복**: NULL과 빈 문자열, 탭·개행·백슬래시, jsonb, bytea, 배열 값이 그대로 옮겨져야 한다. → Task 7 `TestExecute_DataRoundTrip`
5. **prd에서 락 대기**: 다른 세션이 락을 잡고 있으면 `lock_timeout` 안에 명확한 오류로 롤백해야 한다. → Task 7 `TestExecute_LockTimeout`

---

## 파일 구조

```
cmd/pgmigrate/main.go          진입점, -config 플래그, tea.Program 실행
internal/config/config.go      접속 설정 로드, 경로 결정, pgx 설정 생성
internal/catalog/catalog.go    공통 타입, Querier, ListTables, CountRows
internal/catalog/describe.go   Describe(테이블 구조 전체)
internal/catalog/target.go     타깃 사실 조회 (권한, 참조, 의존 뷰, 타입 존재)
internal/ddl/ddl.go            CREATE/DROP/FK/COMMENT 문 생성 (순수 함수)
internal/plan/types.go         Mode, Selection, Issue, Step, Plan
internal/plan/columns.go       컬럼 매칭, 타입 호환성 (순수)
internal/plan/order.go         FK 위상 정렬 (순수)
internal/plan/check.go         Facts → Plan 규칙 검사 (순수)
internal/plan/gather.go        DB에서 Facts 수집
internal/runlog/runlog.go      실행 로그 파일
internal/migrate/migrate.go    실행 엔진
internal/app/backend.go        Backend 인터페이스 + pgx 구현
internal/tui/*.go              화면 (model.go, connect.go, tables.go, preview.go, confirm.go, run.go, result.go, styles.go)
internal/testutil/testutil.go  통합 테스트 헬퍼 (build tag integration)
docker-compose.test.yml        PG 18.4 두 대 (55432 소스, 55433 타깃)
pgmigrate.example.yaml, README.md, build.sh
```

---

### Task 1: 프로젝트 골격 + config

**Files:**
- Create: `go.mod`, `internal/config/config.go`, `internal/config/config_test.go`, `pgmigrate.example.yaml`

**Interfaces:**
- Produces:
  ```go
  type Conn struct {
      Name     string `yaml:"name"`
      Env      string `yaml:"env"`      // "dev" | "qa" | "prd" | 자유 문자열
      Host     string `yaml:"host"`
      Port     int    `yaml:"port"`     // 0이면 5432
      Database string `yaml:"database"`
      User     string `yaml:"user"`
      SSLMode  string `yaml:"sslmode"`  // 빈 값이면 "prefer"
  }
  func (c Conn) IsPrd() bool                           // strings.EqualFold(Env, "prd")
  func Load(path string) ([]Conn, error)               // 최상위 키 connections: [...]
  func ResolvePath(exeDir, appData string, exists func(string) bool) string
  func (c Conn) PgxConfig(password string) (*pgx.ConnConfig, error)
  func SameDatabase(a, b Conn) bool
  ```

- [ ] **Step 1: `go mod init pgmigrate`을 실행하고, `go.mod`에 `go 1.23`을 지정한다. `pgx/v5`와 `gopkg.in/yaml.v3`을 추가한다.**
- [ ] **Step 2: 실패하는 테스트를 작성한다** (`config_test.go`)
  - `TestLoad`: 이름 중복이면 오류. name·host·database·user 중 하나라도 비면 오류. Port 0은 5432, SSLMode 빈 값은 `"prefer"`로 채운다.
  - `TestResolvePath`: `exeDir/pgmigrate.yaml`이 있으면 그 경로를 쓴다. 없으면 `appData/pgmigrate/pgmigrate.yaml`을 쓴다.
  - `TestPgxConfig`: `password=""`이고 `PGPASSWORD=envpw`이면 `cfg.Password == "envpw"`다. `password="pw"`이면 `"pw"`다. host에 공백이나 `'`가 있어도 파싱된다.
  - `TestSameDatabase`: host 대소문자, `localhost`와 `127.0.0.1`은 다르게 본다(단순 비교). port와 database가 같으면 true다.
  - `TestIsPrd`: `"PRD"`이면 true다.
- [ ] **Step 3: 실행 → FAIL** — `go test ./internal/config/`
- [ ] **Step 4: 구현한다.** `PgxConfig`는 keyword/value 문자열(`host='..' port=.. dbname='..' user='..' sslmode=..`, 값 안의 `\`와 `'`는 이스케이프)을 `pgx.ParseConfig`로 파싱한 뒤, `password != ""`일 때만 `cfg.Password`를 덮어쓴다. 비밀번호가 비어 있으면 pgx의 PGPASSWORD/pgpass 처리에 맡긴다. 이어서 `pgmigrate.example.yaml`에 dev/qa/prd 예시 3개를 작성한다.
- [ ] **Step 5: 실행 → PASS**
- [ ] **Step 6: Commit** `feat: config loading`

---

### Task 2: 통합 테스트 환경 + catalog 목록/행 수

**Files:**
- Create: `docker-compose.test.yml`, `internal/testutil/testutil.go`, `internal/catalog/catalog.go`, `internal/catalog/catalog_integration_test.go`

**Interfaces:**
- Produces:
  ```go
  // catalog
  type Querier interface {
      Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error)
      QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
      Exec(ctx context.Context, sql string, args ...any) (pgconn.CommandTag, error)
  }
  type TableRef struct{ Schema, Name string }
  func (r TableRef) Quoted() string   // "schema"."name"
  func (r TableRef) String() string   // schema.name (표시용)
  type TableInfo struct{ Ref TableRef; EstRows int64; Partitioned bool }
  func ListTables(ctx context.Context, q Querier) ([]TableInfo, error) // relkind r,p; pg_catalog/information_schema/pg_toast 제외; schema,name 순
  func CountRows(ctx context.Context, q Querier, ref TableRef) (int64, error)

  // testutil (//go:build integration)
  func Source(t *testing.T) *pgx.Conn  // env PGMIGRATE_TEST_SRC, 기본 postgres://postgres:postgres@localhost:55432/postgres
  func Target(t *testing.T) *pgx.Conn  // env PGMIGRATE_TEST_TGT, 기본 ...:55433/postgres
  func Schema(t *testing.T, conns ...*pgx.Conn) string // 고유 스키마 "t_<rand>"를 모든 conn에 만들고 Cleanup에서 DROP SCHEMA
  func MustExec(t *testing.T, c *pgx.Conn, sql string)
  ```
  통합 테스트를 실행할 때는 각 테스트 SQL의 스키마 이름을 `testutil.Schema` 반환값으로 치환한다.

- [ ] **Step 1: `docker-compose.test.yml`을 작성한다.** `postgres:18.4` 서비스 두 개(`src` 55432, `tgt` 55433)를 두고 `POSTGRES_PASSWORD=postgres`로 설정한다. `docker compose -f docker-compose.test.yml up -d`로 띄운다.
- [ ] **Step 2: testutil을 작성한다.** 접속에 실패하면 `t.Fatalf`로 docker 실행 안내를 출력한다. 테스트 정리(cleanup) 단계의 스키마 삭제에만 `DROP SCHEMA ... CASCADE`를 허용한다. 프로덕션 코드가 아니므로 Global Constraint 예외로 이 줄에 주석을 단다.
- [ ] **Step 3: 실패하는 통합 테스트를 작성한다.**
  - `TestListTables`: 스키마에 `"Order"`, `공통코드`, 파티션 테이블 `p`를 만든다. 세 개 모두 반환되고, `p`만 `Partitioned==true`이며, 정렬 순서를 확인한다.
  - `TestCountRows`: 3행을 넣은 `"Order"`는 3을 반환한다.
- [ ] **Step 4: 실행 → FAIL** — `go test -tags integration ./internal/catalog/`
- [ ] **Step 5: 구현한다.** `EstRows`는 `pg_class.reltuples`를 쓰고, 음수면 0으로 한다.
- [ ] **Step 6: 실행 → PASS**, 그리고 `go test ./...`(태그 없음)도 PASS
- [ ] **Step 7: Commit** `feat: catalog listing and test infra`

---

### Task 3: catalog.Describe

**Files:**
- Create: `internal/catalog/describe.go`, `internal/catalog/describe_integration_test.go`

**Interfaces:**
- Consumes: Task 2의 `Querier`, `TableRef`
- Produces:
  ```go
  var ErrNotFound = errors.New("table not found")
  type Column struct {
      Name          string
      Type          string // format_type(atttypid, atttypmod)
      TypeSchema    string // 타입의 스키마
      TypeName      string // pg_type.typname
      TypeKind      byte   // pg_type.typtype: 'b','e','d','c','r','m'
      UserType      bool   // TypeSchema가 pg_catalog가 아님
      NotNull       bool
      Default       string // pg_get_expr(adbin); 없으면 "" (generated 컬럼이면 "")
      Identity      byte   // 0, 'a'(ALWAYS), 'd'(BY DEFAULT)
      Generated     byte   // 0, 's'(STORED), 'v'(VIRTUAL)
      GeneratedExpr string
      Collation     string // 타입 기본과 다를 때만 인용된 이름, 아니면 ""
      Comment       string
  }
  type Constraint struct {
      Name       string
      Type       byte   // 'p','u','c','f','x' (NOT NULL 'n'은 제외)
      Def        string // pg_get_constraintdef(oid, true)
      RefTable   *TableRef // Type=='f'일 때
      Deferrable bool
  }
  type Index struct{ Name, Def string } // 제약조건 소유 인덱스 제외
  type Sequence struct {
      Ref        TableRef
      Column     string
      Identity   bool   // pg_depend deptype 'i'
      DataType   string // pg_sequence.seqtypid의 format_type
      Start, Increment, Min, Max, Cache int64
      Cycle      bool
      LastValue  int64
      IsCalled   bool
  }
  type Table struct {
      Ref          TableRef
      Columns      []Column      // attnum 순, dropped 제외
      Constraints  []Constraint  // 이름 순
      Indexes      []Index       // 이름 순
      Sequences    []Sequence    // 소유 시퀀스 (deptype 'a' 또는 'i')
      Comment      string
      Unlogged     bool
      Partitioned  bool          // relkind 'p'
      IsPartition  bool          // relispartition
      Inherits     bool          // pg_inherits에서 부모 또는 자식
      TriggerCount int           // tgisinternal = false
      HasRLS       bool          // relrowsecurity
  }
  func Describe(ctx context.Context, q Querier, ref TableRef) (*Table, error)
  ```

- [ ] **Step 1: 실패하는 통합 테스트를 작성한다.** 픽스처 테이블 `"Order"`는 다음을 갖는다.
  - `id serial PRIMARY KEY`
  - `code varchar(20) NOT NULL UNIQUE`
  - `amt numeric(12,2) DEFAULT 0 CHECK (amt >= 0)`
  - `nm text COLLATE "C"`
  - `g int GENERATED ALWAYS AS (id*2) STORED`
  - `v int GENERATED ALWAYS AS (id+1) VIRTUAL`
  - `parent_id int REFERENCES "Order"(id)`
  - 추가 인덱스 `idx_nm`, 테이블·컬럼 코멘트, 트리거 1개

  두 번째 테이블 `공통코드`는 `id int GENERATED BY DEFAULT AS IDENTITY (START 100 INCREMENT 5)`와 enum 컬럼 `st mood`를 갖는다. 이 테이블에서 `nextval`을 2회 호출한다.
  - `TestDescribe_Columns`: 각 필드 값을 확인한다. `g.Generated=='s'`, `v.Generated=='v'`, `id.Default`에 `nextval(` 포함, `nm.Collation=="\"C\""`, `st.UserType && TypeKind=='e'`를 검증한다.
  - `TestDescribe_Constraints`: p, u, c, f 4개가 있고 'n'은 없다. FK의 `RefTable`은 자기 자신이다.
  - `TestDescribe_Indexes`: `idx_nm` 하나만 나온다. PK와 UNIQUE 인덱스는 제외된다.
  - `TestDescribe_Sequences`: `"Order"`는 serial 시퀀스(Identity=false)다. `공통코드`는 Identity=true, Start=100, Increment=5, `LastValue=105`, `IsCalled=true`다.
  - `TestDescribe_Flags`: TriggerCount=1. 상속 자식 테이블은 `Inherits=true`다.
  - `TestDescribe_NotFound`: `errors.Is(err, ErrNotFound)`
- [ ] **Step 2: 실행 → FAIL**
- [ ] **Step 3: 구현한다.** 시퀀스 현재 값은 `SELECT last_value, is_called FROM <seq>`로 읽는다. 옵션은 `pg_sequence`에서 가져온다.
- [ ] **Step 4: 실행 → PASS**
- [ ] **Step 5: Commit** `feat: catalog describe`

---

### Task 4: catalog 타깃 사실 조회

**Files:**
- Create: `internal/catalog/target.go`, `internal/catalog/target_integration_test.go`

**Interfaces:**
- Produces:
  ```go
  type Privileges struct{ Select, Insert, Truncate, Owner bool } // Owner = pg_has_role(relowner,'USAGE')
  func TablePrivileges(ctx context.Context, q Querier, ref TableRef) (Privileges, error)
  func SchemaExists(ctx context.Context, q Querier, schema string) (bool, error)
  func SchemaCreatePrivilege(ctx context.Context, q Querier, schema string) (bool, error)
  func TypeExists(ctx context.Context, q Querier, schema, name string) (bool, error)
  func TableExists(ctx context.Context, q Querier, ref TableRef) (bool, error)
  func ReferencingTables(ctx context.Context, q Querier, ref TableRef) ([]TableRef, error) // ref를 FK로 참조하는 다른 테이블 (자기 자신 제외)
  func DependentViews(ctx context.Context, q Querier, ref TableRef) ([]TableRef, error)    // pg_depend+pg_rewrite
  func SequenceUpdatePrivilege(ctx context.Context, q Querier, seq TableRef) (bool, error)
  ```

- [ ] **Step 1: 실패하는 통합 테스트를 작성한다.** 타깃에 SELECT만 가진 롤 `ro_<rand>`를 만들고, 그 롤로 `SET ROLE`해서 `Insert/Truncate/Owner == false`를 확인한다. 참조 테이블과 뷰를 만들어 `ReferencingTables`와 `DependentViews`의 반환값을 확인한다. `TypeExists`는 enum이 있을 때와 없을 때를 모두 확인한다.
- [ ] **Step 2: 실행 → FAIL**
- [ ] **Step 3: 구현한다.** `has_table_privilege`와 `has_schema_privilege`, `has_sequence_privilege`를 쓴다.
- [ ] **Step 4: 실행 → PASS**
- [ ] **Step 5: Commit** `feat: target catalog facts`

---

### Task 5: DDL 생성

**Files:**
- Create: `internal/ddl/ddl.go`, `internal/ddl/ddl_test.go`, `internal/ddl/ddl_integration_test.go`

**Interfaces:**
- Consumes: `catalog.Table`, `Column`, `Constraint`, `Index`, `Sequence`
- Produces:
  ```go
  func CreateTable(t *catalog.Table) []string // 순서: CREATE SEQUENCE(비 identity) → CREATE TABLE → ALTER SEQUENCE OWNED BY → CREATE INDEX → COMMENT
  func ForeignKeys(t *catalog.Table) []string // ALTER TABLE .. ADD CONSTRAINT .. <Def> (FK만)
  func DropTable(ref catalog.TableRef) string // DROP TABLE "s"."t"  (CASCADE 없음)
  func SetSequence(seq catalog.TableRef, value int64, isCalled bool) string // SELECT setval('<quoted>'::regclass, value, isCalled) — 값은 정수 리터럴
  ```

생성 규칙은 아래와 같다. 이 규칙을 그대로 테스트 기대값으로 쓴다.
- `CREATE [UNLOGGED] TABLE "s"."t" (` 다음에 컬럼을 쓰고, 그 뒤에 FK를 뺀 `CONSTRAINT "n" <Def>`들을 붙인 뒤 `)`로 닫는다.
- 컬럼은 `"col" <Type>` 뒤에 아래 항목을 이 순서로 붙인다.
  1. `COLLATE <Collation>`
  2. generated가 있으면 `GENERATED ALWAYS AS (<expr>) STORED|VIRTUAL`, 없으면 `DEFAULT <Default>`
  3. identity면 `GENERATED ALWAYS|BY DEFAULT AS IDENTITY (SEQUENCE NAME "s"."seq" START WITH a INCREMENT BY b MINVALUE c MAXVALUE d CACHE e [NO ]CYCLE)`
  4. `NOT NULL` (identity가 아닐 때)
- serial 시퀀스는 `CREATE SEQUENCE "s"."seq" AS <DataType> START WITH .. INCREMENT BY .. MINVALUE .. MAXVALUE .. CACHE .. [NO ]CYCLE`로 만든다. 테이블 생성 후 `ALTER SEQUENCE "s"."seq" OWNED BY "s"."t"."col"`을 실행한다.
- 코멘트는 `COMMENT ON TABLE|COLUMN .. IS <리터럴>`로 쓴다. 작은따옴표는 두 번 써서 이스케이프하고, 백슬래시가 있으면 `E''` 대신 표준 문자열(standard_conforming_strings=on 전제)을 쓴다.

- [ ] **Step 1: 실패하는 단위 테스트를 작성한다.**
  - `TestCreateTable_Serial`: 문 순서와 시퀀스·OWNED BY 문을 정확한 문자열로 비교한다.
  - `TestCreateTable_Identity`: identity 절이 들어가고 CREATE SEQUENCE는 없다.
  - `TestCreateTable_Generated`: STORED와 VIRTUAL 각각, DEFAULT가 함께 나오지 않는다.
  - `TestCreateTable_QuotedNames`: `"Order"`, `공통코드`, 컬럼 `"user"`가 올바르게 인용된다.
  - `TestCreateTable_ExcludesFK`: CREATE TABLE에 FK가 없고, `ForeignKeys`에 FK가 있다.
  - `TestComment_Escaping`: `it's` → `'it''s'`
  - `TestSetSequence`
- [ ] **Step 2: 실행 → FAIL** — `go test ./internal/ddl/`
- [ ] **Step 3: 구현한다.**
- [ ] **Step 4: 실행 → PASS**
- [ ] **Step 5: 왕복 통합 테스트를 작성한다.** `TestRoundTrip`은 Task 3 픽스처를 소스에서 Describe하고, `CreateTable`과 `ForeignKeys`를 타깃 같은 스키마에 실행한다. 그 뒤 타깃을 Describe해서 아래를 비교한다.
  - Columns, Constraints(Def), Indexes(Def), Sequences 옵션
  - TriggerCount는 0이어야 한다.
  - enum `mood`는 테스트에서 타깃에 미리 만든다.
- [ ] **Step 6: 실행 → PASS** — `go test -tags integration ./internal/ddl/`
- [ ] **Step 7: Commit** `feat: ddl generation`

---

### Task 6: plan — 타입, 컬럼 매칭, 정렬, 규칙 검사, 사실 수집

**Files:**
- Create: `internal/plan/types.go`, `columns.go`, `order.go`, `check.go`, `gather.go`, `columns_test.go`, `order_test.go`, `check_test.go`, `gather_integration_test.go`

**Interfaces:**
- Consumes: `catalog.*`, `ddl.CreateTable`, `ddl.ForeignKeys`, `config.Conn`
- Produces:
  ```go
  type Mode string
  const (ModeCreate Mode = "create"; ModeTruncate Mode = "truncate"; ModeAppend Mode = "append")
  type Selection struct{ Ref catalog.TableRef; Mode Mode; DropRecreate bool }
  type Severity int
  const (SevInfo Severity = iota; SevWarning; SevError)
  type Issue struct{ Table *catalog.TableRef; Severity Severity; Message string }
  type Step struct {
      Sel        Selection
      Source     *catalog.Table
      Target     *catalog.Table // 실행 전 타깃 구조 (create이고 없으면 nil)
      Columns    []string       // COPY 컬럼 (generated 제외, 소스 순서)
      DDL        []string       // create일 때 ddl.CreateTable 결과
      FKDDL      []string       // create일 때 ddl.ForeignKeys 결과
      SourceRows int64
      TargetRows int64          // 미리보기 시점 (없으면 0)
  }
  type Plan struct {
      Source, Target config.Conn
      Steps  []Step   // 부모→자식 순
      Issues []Issue
  }
  func (p *Plan) HasErrors() bool
  func (p *Plan) HasDestructive() bool // truncate 또는 DropRecreate 포함

  func MatchColumns(src, tgt []catalog.Column) (cols []string, problems []string)
  func TypeCompatible(src, tgt catalog.Column) bool
  var ErrCycle = errors.New("FK cycle")
  func Order(tables map[catalog.TableRef]*catalog.Table, sel []catalog.TableRef) ([]catalog.TableRef, error)

  type TargetFacts struct {
      Table           *catalog.Table // nil = 없음
      SchemaExists    bool
      SchemaCreate    bool
      Priv            catalog.Privileges
      ReferencedBy    []catalog.TableRef
      DependentViews  []catalog.TableRef
      MissingTypes    []string           // "schema.name"
      MissingFKTables []catalog.TableRef // create 시 선택 밖 FK 대상 중 타깃에 없는 것
      SeqUpdate       map[catalog.TableRef]bool
      Rows            int64
  }
  type Facts struct {
      Source     map[catalog.TableRef]*catalog.Table
      SourceRows map[catalog.TableRef]int64
      Target     map[catalog.TableRef]*TargetFacts
  }
  func Check(src, tgt config.Conn, sel []Selection, f *Facts) *Plan
  func Gather(ctx context.Context, src, tgt catalog.Querier, sel []Selection) (*Facts, error)
  func Build(ctx context.Context, src, tgt catalog.Querier, srcConn, tgtConn config.Conn, sel []Selection) (*Plan, error) // Gather + Check
  ```

규칙은 아래와 같다. 각 규칙에 테스트를 하나씩 둔다. 메시지는 한국어로 쓴다.

| # | 조건 | 결과 |
|---|---|---|
| R1 | `config.SameDatabase(src,tgt)` | Error (전체) |
| R2 | 소스 또는 타깃 테이블이 Partitioned/IsPartition/Inherits | Error |
| R3 | 타깃 스키마 없음 | Error |
| R4 | create, 타깃에 있음, DropRecreate=false | Error |
| R5 | create, DropRecreate, `tgt.IsPrd()` | Error |
| R6 | create, DropRecreate, 비 prd | Warning (GRANT/OWNER/트리거/RLS 소실) + DependentViews 또는 선택 밖 ReferencedBy 있으면 Error. DROP하려면 Priv.Owner 필요 (없으면 Error) |
| R7 | create: MissingTypes / MissingFKTables 있음 | Error |
| R8 | create: !SchemaCreate | Error |
| R9 | create: 소스에 TriggerCount>0 / HasRLS | Warning (생성 안 됨) |
| R10 | truncate/append, 타깃에 없음 | Error |
| R11 | truncate/append: MatchColumns problems | Error (문제별) |
| R12 | truncate: 선택 밖 ReferencedBy | Error |
| R13 | truncate: !Priv.Truncate / truncate·append: !Priv.Insert | Error |
| R14 | truncate/append: 타깃 TriggerCount>0 | Info (트리거가 실행됨) |
| R15 | 소유 시퀀스 SeqUpdate=false (truncate/append) | Error |
| R16 | `Order`가 ErrCycle | Error |
| R17 | `HasDestructive()` | Warning (빨간 강조용) |

`MatchColumns` 규칙:
- generated 컬럼은 소스와 타깃 양쪽 모두에서 제외한다.
- 소스 컬럼이 타깃에 없으면 problem으로 남긴다.
- `!TypeCompatible`이면 problem으로 남긴다.
- 타깃에만 있는 컬럼이 `NotNull && Default=="" && Identity==0`이면 problem으로 남긴다.

`TypeCompatible` 규칙:
- `Type`이 같으면 true다.
- 소스 `character varying(n)`, 타깃 `character varying(m)`이고 m≥n이면 true다.
- 소스 `character varying(n)` 또는 `character varying`, 타깃 `text`면 true다.
- 타깃 `character varying`(길이 없음)이고 소스가 varchar면 true다.
- enum/domain 비교는 `TypeSchema+TypeName`으로 한다. `Type` 문자열 비교로도 충분하다.

`Order` 규칙:
- 선택 안에서의 FK만 간선으로 본다. 자기참조는 무시한다.
- 같은 레벨에서는 `schema.name` 사전순으로 정렬해 결정적인 결과를 낸다.

- [ ] **Step 1: 실패하는 단위 테스트를 작성한다.**
  - `TestTypeCompatible`: 표로 작성한다. `varchar(10)→varchar(50)` true, `varchar(50)→varchar(10)` false, `varchar(10)→text` true, `int4→int8` false, `numeric(10,2)→numeric(12,2)` false.
  - `TestMatchColumns`
  - `TestOrder`: 부모→자식, 자기참조 허용, A↔B 순환은 ErrCycle.
  - `TestCheck_R1`부터 `TestCheck_R17`까지: 각 규칙마다 Facts를 손으로 만든다.
  - `TestCheck_HappyPath`: truncate 1개와 append 1개, 오류 없음. Steps 순서와 Columns를 확인한다.
- [ ] **Step 2: 실행 → FAIL** — `go test ./internal/plan/`
- [ ] **Step 3: 구현한다.** types, columns, order, check 순서로 진행한다.
- [ ] **Step 4: 실행 → PASS**
- [ ] **Step 5: `gather_integration_test.go`를 작성한다.**
  - `TestBuild_Integration`: 소스에 부모/자식 테이블과 enum 컬럼을 둔다. 타깃에는 부모만 있다. 선택은 부모 truncate와 자식 create다. 기대 결과는 다음과 같다.
    - 자식에 R7 오류(enum 없음)가 나야 한다.
    - 타깃에 enum을 만들면 오류가 없어야 한다.
    - Steps는 부모 → 자식 순이어야 한다.
- [ ] **Step 6: Gather를 구현하고 → PASS**
- [ ] **Step 7: Commit** `feat: migration planning`

---

### Task 7: runlog + 실행 엔진

**Files:**
- Create: `internal/runlog/runlog.go`, `internal/runlog/runlog_test.go`, `internal/migrate/migrate.go`, `internal/migrate/migrate_integration_test.go`

**Interfaces:**
- Consumes: `plan.Plan`, `plan.Step`, `ddl.*`, `catalog.CountRows`
- Produces:
  ```go
  // runlog
  type Logger interface{ Printf(format string, args ...any) }
  type File struct{ /* ... */ }
  func Open(dir string, now time.Time) (*File, error) // dir 생성, 파일명 pgmigrate-20061102-150405.log
  func (f *File) Printf(format string, args ...any)   // 각 줄 앞에 RFC3339 시각
  func (f *File) Path() string
  func (f *File) Close() error
  func Discard() Logger

  // migrate
  type Options struct{ LockTimeout time.Duration } // 0이면 10s
  type Phase string
  const (PhaseStart Phase = "start"; PhaseDDL = "ddl"; PhaseTruncate = "truncate"; PhaseCopy = "copy"; PhaseVerify = "verify"; PhaseSequence = "sequence"; PhaseCommit = "commit"; PhaseDone = "done"; PhaseFailed = "failed")
  type Event struct{ Table *catalog.TableRef; Phase Phase; Rows int64; Err error }
  type TableResult struct {
      Ref                catalog.TableRef
      Mode               plan.Mode
      SourceRows, Before, After int64
      Duration           time.Duration
  }
  type Result struct{ Tables []TableResult; Committed bool }
  func Execute(ctx context.Context, src, tgt *pgx.Conn, p *plan.Plan, opt Options, log runlog.Logger, onEvent func(Event)) (*Result, error)
  ```

`Execute` 단계 순서는 아래와 같다. 실행한 SQL은 모두 log에 남긴다.
1. `p.HasErrors()`이면 즉시 오류를 반환하고 DB는 건드리지 않는다.
2. 소스에서 `BEGIN ISOLATION LEVEL REPEATABLE READ READ ONLY`를 실행한다.
3. 타깃에서 `BEGIN` 후 `SET LOCAL lock_timeout = '<ms>ms'`를 실행한다.
4. DropRecreate 단계를 Steps 역순으로 돌며 `ddl.DropTable`을 실행한다.
5. truncate/append 대상을 한 문장 `LOCK TABLE a, b IN EXCLUSIVE MODE`로 잠그고, 각 테이블의 `Before`를 CountRows로 센다.
6. create 단계마다 `Step.DDL`을 실행한다.
7. truncate 대상을 한 문장 `TRUNCATE a, b`로 비운다.
8. Steps 순서대로 COPY를 실행한다.
   - `io.Pipe`로 연결해, 고루틴에서 `src.PgConn().CopyTo(ctx, pw, "COPY (SELECT cols FROM t) TO STDOUT")`를 실행하고 그 결과로 `pw.CloseWithError(err)`를 호출한다.
   - 본 흐름에서는 `tgt.PgConn().CopyFrom(ctx, pr, "COPY t (cols) FROM STDIN")`를 실행한다.
   - 타깃 쪽이 실패하면 `pr.CloseWithError`로 소스 고루틴을 풀어 준다.
   - 두 오류를 모두 확인한다.
   - `SourceRows`는 소스 CopyTo의 CommandTag `RowsAffected`로 정한다.
9. create 단계마다 `Step.FKDDL`을 실행한다.
10. 검증한다. `After = CountRows`를 세고, create/truncate는 `After == SourceRows`, append는 `After == Before + SourceRows`여야 한다. 다르면 오류로 처리한다.
11. 시퀀스를 맞춘다. `next(last, isCalled, inc) = isCalled ? last+inc : last`로 정의한다.
    - create는 소스 값 그대로 `setval(seq, last, isCalled)`를 실행한다.
    - truncate/append는 타깃 현재 값을 `SELECT last_value, is_called` 트랜잭션 안에서 읽는다. 소스와 타깃 중 `next`가 큰 쪽의 (last, isCalled)로 setval한다.
12. 타깃을 `COMMIT`하고 소스를 `ROLLBACK`한다. `Committed=true`.
- 어느 단계에서든 오류가 나면 `context.Background()`로 타깃을 ROLLBACK하고, 소스도 ROLLBACK한다. 이어서 `PhaseFailed` 이벤트를 보내고, 오류를 `"<table>: <phase>: %w"` 형태로 감싸 반환한다.

- [ ] **Step 1: runlog 단위 테스트를 작성한다.** `TestOpen`에서 파일명 형식과 Printf 줄 형식을 확인한다. 이어서 구현하고 PASS시킨다.
- [ ] **Step 2: 실패하는 통합 테스트를 작성한다.** 각 테스트는 Task 6의 `plan.Build`로 계획을 만든다.
  - `TestExecute_Create`: Task 3 픽스처를 옮긴다. 행 수, 데이터, serial `nextval`이 소스 다음 값과 같은지 확인한다. identity ALWAYS 컬럼 값도 보존돼야 한다.
  - `TestExecute_DropRecreate`: 비 prd 타깃에서 기존 테이블이 교체되는지 확인한다.
  - `TestExecute_Truncate`: 타깃 기존 5행이 소스 3행으로 바뀐다. 타깃 시퀀스가 더 높으면 낮아지지 않는다.
  - `TestExecute_Append`: 기존 2행에 소스 3행이 더해져 5행이 된다. 시퀀스는 max 규칙을 따른다.
  - `TestExecute_AppendConflictRollsBack`: PK 충돌이 나면 오류를 반환한다. 앞서 처리된 다른 테이블까지 포함해 타깃이 원래대로 남고, 시퀀스 값도 변하지 않아야 한다.
  - `TestExecute_FKOrder`: 부모와 자식을 함께 truncate해도 성공한다.
  - `TestExecute_DataRoundTrip`: NULL, `''`, `E'a\tb\nc\\d'`, jsonb, bytea, `text[]`, 한글 값이 바이트 단위로 같아야 한다. 컬럼명 `"user"`, 테이블 `공통코드`를 쓴다.
  - `TestExecute_LockTimeout`: 다른 연결이 `LOCK TABLE ... ACCESS EXCLUSIVE`를 잡고 있고 `LockTimeout=300ms`이면 2초 안에 오류가 나고 타깃은 그대로다.
  - `TestExecute_SourceFailureRollsBack`: 소스 COPY가 몇 행을 보낸 뒤 실패하는 상황을 만든다.
    - 패키지 변수 `var copySelectSQL = func(s plan.Step) string`(기본값은 `SELECT cols FROM t`)을 테스트에서 덮어쓴다.
    - 덮어쓴 쿼리는 `SELECT CASE WHEN g < 3 THEN g ELSE 1/(g-g) END FROM generate_series(1,5) g`다.
    - 오류를 반환하고, 타깃은 그대로이며, 고루틴 누수가 없어야 한다. 누수는 테스트 끝에 `runtime.NumGoroutine`을 비교해 확인한다.
  - `TestExecute_CancelRollsBack`: 첫 `PhaseCopy` 이벤트에서 cancel하면 `context.Canceled` 오류가 나고 타깃은 그대로다.
  - `TestExecute_RefusesPlanWithErrors`
- [ ] **Step 3: 실행 → FAIL** — `go test -tags integration ./internal/migrate/`
- [ ] **Step 4: Execute를 구현한다.**
- [ ] **Step 5: 실행 → PASS.** `-count=3`으로 반복해도 안정적이어야 한다.
- [ ] **Step 6: Commit** `feat: migration executor`

---

### Task 8: app.Backend

**Files:**
- Create: `internal/app/backend.go`, `internal/app/backend_integration_test.go`

**Interfaces:**
- Consumes: `config`, `catalog.ListTables`, `plan.Build`, `migrate.Execute`, `runlog.Open`
- Produces:
  ```go
  type Backend interface {
      Connect(ctx context.Context, src, tgt config.Conn, srcPassword, tgtPassword string) error
      ListTables(ctx context.Context) ([]catalog.TableInfo, error) // 소스 기준
      Plan(ctx context.Context, sel []plan.Selection) (*plan.Plan, error)
      Execute(ctx context.Context, p *plan.Plan, onEvent func(migrate.Event)) (res *migrate.Result, logPath string, err error)
      Close()
  }
  func NewPG(logDir string, opt migrate.Options) Backend
  ```
  `Execute`는 실행마다 `runlog.Open(logDir, time.Now())`를 호출한다. 로그 머리말에는 소스·타깃의 name, env, host, port, database, user를 쓰고 비밀번호는 쓰지 않는다. 테이블별 모드와 이슈 목록도 함께 기록한다.

- [ ] **Step 1: 실패하는 통합 테스트를 작성한다.** `TestBackend_EndToEnd`는 Connect → ListTables → Plan → Execute 순으로 호출한다. 로그 파일이 존재하고 비밀번호 문자열을 포함하지 않는지 확인한다.
- [ ] **Step 2: 구현하고 → PASS**
- [ ] **Step 3: Commit** `feat: backend wiring`

---

### Task 9: TUI

**Files:**
- Create: `internal/tui/model.go`, `styles.go`, `connect.go`, `tables.go`, `preview.go`, `confirm.go`, `run.go`, `result.go`, `internal/tui/tui_test.go`, `internal/tui/fake_test.go`

**Interfaces:**
- Consumes: `app.Backend`, `config.Conn`, `plan.*`, `migrate.Event/Result`
- Produces: `func New(b app.Backend, conns []config.Conn) tea.Model`

화면과 키 동작은 다음과 같다(설계 8장).
- **connect**: 소스 목록에서 Enter → 타깃 목록에서 Enter → 비밀번호 입력 2개(마스킹, 빈 값 허용) → Enter로 Connect 실행.
  - env 색상: prd 빨강, qa 노랑, 그 외 초록.
  - `SameDatabase`면 타깃을 고를 수 없다는 메시지를 띄운다.
- **tables**: 목록에 `[x] schema.name  mode  ~rows`를 표시한다.
  - `/` 필터, `space` 선택, `c`/`t`/`a` 모드 지정, `d` DropRecreate 토글(타깃 prd면 무시하고 메시지 표시).
  - Enter → `Backend.Plan` 호출 → preview.
  - 기본 모드는 truncate.
- **preview**: Steps 순서대로 모드, 소스 행 수, 타깃 행 수를 표시한다.
  - Issues는 심각도별 색으로 보여 준다. `HasDestructive`면 상단에 빨간 배너를 띄운다.
  - `v`로 DDL을 펼친다.
  - `HasErrors()`면 Enter를 막고 "오류를 해결해야 실행할 수 있습니다"를 표시한다. Esc는 tables로 돌아간다.
- **confirm**: 타깃이 prd면 `textinput`에 타깃 database 이름을 정확히 입력해야 Enter가 동작한다. 아니면 `y`/`n`으로 확인한다.
- **run**: 테이블별 상태(대기/진행/완료/실패)를 보여 준다.
  - 이벤트는 `tea.Cmd`로 채널에서 받는다.
  - `ctrl+c`는 실행 컨텍스트를 cancel하고 결과를 기다린다. 앱은 바로 종료하지 않는다.
- **result**: Committed 여부, 테이블별 SourceRows/Before/After/Duration, 오류, 로그 경로를 보여 준다. `q`로 종료한다.
- 실행 중이 아닐 때 `ctrl+c`/`q`는 바로 종료한다.

- [ ] **Step 1: `fake_test.go`에 fakeBackend를 만든다.** 호출을 기록하고, 정해 둔 Plan/Result/이벤트를 반환한다. Execute는 ctx 취소를 존중한다.
- [ ] **Step 2: 실패하는 모델 테스트를 작성한다.** `tea.KeyMsg`를 차례로 `Update`에 넣고, 반환된 Cmd를 동기 실행하는 헬퍼를 둔다.
  - `TestConnect_SameDatabaseBlocked`
  - `TestTables_DropRecreateBlockedOnPrd`
  - `TestTables_ModeKeys`: Plan 호출 시 Selection 값을 확인한다.
  - `TestPreview_ErrorsBlockExecute`: Enter를 눌러도 Execute가 호출되지 않는다.
  - `TestConfirm_PrdRequiresExactDBName`: 틀린 이름이면 진행되지 않고, 정확한 이름이면 Execute가 호출된다.
  - `TestConfirm_NonPrdYes`
  - `TestRunScreen_CtrlCCancels`: fake Execute가 받은 ctx가 취소되고, 화면은 result로 넘어가며 Committed=false를 표시한다.
  - `TestResult_ShowsLogPath`: View 문자열에 경로가 들어 있다.
- [ ] **Step 3: 실행 → FAIL** — `go test ./internal/tui/`
- [ ] **Step 4: 화면을 구현한다.**
- [ ] **Step 5: 실행 → PASS**
- [ ] **Step 6: Commit** `feat: tui screens`

---

### Task 10: 진입점, 빌드, 문서, 실사용 확인

**Files:**
- Create: `cmd/pgmigrate/main.go`, `build.sh`, `README.md`

**Interfaces:**
- Consumes: `config.ResolvePath`, `config.Load`, `app.NewPG`, `tui.New`

- [ ] **Step 1: `main.go`를 작성한다.**
  - 플래그는 `-config <path>`와 `-version`이다.
  - config 경로는 플래그를 우선하고, 없으면 `ResolvePath(exe 디렉터리, os.Getenv("APPDATA"), fileExists)`로 정한다.
  - 로그 디렉터리는 `<exe 디렉터리>/logs`다.
  - `tea.NewProgram(tui.New(...), tea.WithAltScreen())`로 실행한다.
  - 설정 파일이 없으면 예시 경로를 안내하고 종료 코드 2로 끝낸다.
- [ ] **Step 2: `build.sh`를 작성한다.** `GOTOOLCHAIN=local CGO_ENABLED=0 GOOS=windows GOARCH=amd64 go build -trimpath -ldflags "-s -w -X main.version=$(git describe --always --dirty)" -o dist/pgmigrate.exe ./cmd/pgmigrate`를 실행하고, 같은 방식으로 darwin 바이너리도 만든다.
- [ ] **Step 3: 빌드를 검증한다.**
  - `./build.sh && file dist/pgmigrate.exe` 결과에 `PE32+ executable ... x86-64`가 나와야 한다.
  - `go vet ./...`가 통과해야 한다.
  - `go test ./...`가 통과해야 한다.
  - `go test -tags integration ./...`가 통과해야 한다.
- [ ] **Step 4: 실사용을 확인한다.** docker 두 DB를 가리키는 `pgmigrate.yaml`을 만든다(타깃 env=prd). darwin 바이너리를 tmux에서 띄운다.
  - truncate 1개, create 1개를 선택하고 미리보기 → DB 이름 입력 → 결과까지 진행한다.
  - 행 수와 로그 파일을 확인한다.
  - 진행 화면 캡처를 남긴다.
- [ ] **Step 5: `README.md`를 작성한다.** 다음 내용을 담는다.
  - 설치(exe 하나)와 설정 파일 위치와 예시
  - 비밀번호 처리(PGPASSWORD, `%APPDATA%\postgresql\pgpass.conf`)
  - 모드 설명과 안전 장치
  - 로그 위치
  - 통합 테스트 실행 방법
- [ ] **Step 6: Commit** `feat: entrypoint, build script, docs`
