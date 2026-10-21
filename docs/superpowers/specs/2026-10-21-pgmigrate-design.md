# pgmigrate 설계 문서

작성일: 2026-10-21

## 1. 목적과 배경

dev → qa → prd 순서로 배포할 때, 작은 테이블(공통코드, 설정 등 기준 데이터로 추정)을
한 DB에서 다른 DB로 옮기는 작업을 TUI로 안전하게 하기 위한 도구.

- Windows에서 실행되는 **단일 exe** (설치/DLL 없음)
- 언어: **Go**
- 소스/타깃 모두 **PostgreSQL 18.4**

## 2. 목표 / 비목표 / 반(反)목표

### 목표
- 소스 접속 1개, 타깃 접속 1개를 고르고, 여러 테이블을 선택해 테이블별 모드로 한 번에 실행
- 모드 3가지 지원: **create / truncate / append**
- 실행 전 미리보기, 실행 후 행 수 검증
- prd 대상 작업에 대한 실수 방지 장치

### 비목표 (v1에서 하지 않음)
- 대용량 테이블 최적화 (작은 테이블만 대상)
- 서로 다른 PG 버전 간 마이그레이션
- DB 전체/스키마 전체 덤프, 함수·뷰·타입·확장 등 테이블 외 객체 이전
- 스키마 차이 비교 및 ALTER 생성
- 증분 동기화/CDC, upsert·skip 같은 충돌 처리 옵션 (나중에 추가 가능하도록만 설계)

### 반목표 (기술적으로 동작해도 실패로 보는 것)
- prd 데이터를 의도치 않게 지우거나 덮어쓰는 것
- 실패했는데 타깃에 일부만 반영된 상태로 남는 것
- 행 수가 안 맞는데 성공으로 보고하는 것

## 3. 제약
- pg_dump/pg_restore 및 libpq에 의존하지 않음. DB 접속은 순수 Go 드라이버 **pgx v5**
- TUI: **Bubble Tea + Bubbles + Lip Gloss**
- 빌드: `GOOS=windows GOARCH=amd64 CGO_ENABLED=0 go build` (macOS에서 크로스 빌드). 개발/테스트는 macOS에서도 실행 가능해야 함
- 스키마(DDL)는 서버 함수 `pg_get_*def()` 등 카탈로그 조회로 Go에서 직접 생성

## 4. 사용자가 정한 것

- Go, TUI, 단일 바이너리
- 18.4 → 18.4, 작은 테이블만
- create / truncate / append 세 모드 모두 지원
- 아래 5장의 모드 동작 정의(대화에서 제시한 내용 그대로 승인됨)
- 프로젝트 위치: `~/wjgit/pgmigrate`

## 5. 모드 동작

| 모드 | 타깃에 테이블이 없을 때 | 있을 때 |
|---|---|---|
| create | 테이블 생성 후 데이터 복사 | **오류**. 단, "DROP 후 재생성" 옵션을 켜고 별도 확인하면 DROP → 생성 → 복사 |
| truncate | 오류 | `TRUNCATE` 후 소스 전체 데이터 복사 |
| append | 오류 | 기존 데이터 유지, 소스 행 추가. PK/UNIQUE 충돌 시 실패 → 롤백 |

### create 시 만드는 것
- 컬럼: 이름, 타입(`format_type`), NOT NULL, DEFAULT(`pg_get_expr`), collation(기본과 다를 때)
- identity 컬럼(`attidentity`, 옵션 포함), generated 컬럼(`attgenerated`, 식 포함)
- 제약조건: PK, UNIQUE, CHECK, FK, EXCLUDE (`pg_get_constraintdef`)
- 인덱스: 제약조건이 만드는 인덱스를 제외한 나머지 (`pg_get_indexdef`)
- 테이블이 소유한 시퀀스(serial/identity) 및 그 값
- 테이블·컬럼 코멘트

### create 시 만들지 않는 것 (미리보기에 경고로 표시)
- 트리거 (트리거 함수가 타깃에 없을 수 있음), RLS 정책, GRANT, OWNER (접속 사용자 소유로 생성)
- unlogged 여부 외 특수 storage 옵션
- **파티션/상속 테이블(부모·자식 모두)은 모든 모드에서 오류** [내 결정]
- create 대상 컬럼이 쓰는 enum/domain/확장 타입이 타깃에 없으면 **미리보기에서 오류** [내 결정]

## 6. 실행 규칙 [내 결정]

### 트랜잭션
- **타깃: 한 번의 실행 전체를 하나의 트랜잭션**으로 처리. 테이블 하나라도 실패하면 전부 롤백 (all-or-nothing). PG는 DDL도 트랜잭션이므로 create 포함 가능
- **소스: `REPEATABLE READ READ ONLY` 트랜잭션**으로 모든 테이블을 같은 시점 기준으로 읽음
- `lock_timeout` 설정(기본 10초, 설정 가능)으로 prd 락 대기에 무한정 걸리지 않게 함
- 실행 시작 시 대상 테이블을 모두 `LOCK TABLE ... IN EXCLUSIVE MODE`로 잠가, 실행 중 다른 세션의 쓰기를 막음(읽기는 허용). 기존 행 수는 락을 잡은 뒤에 셈 [내 결정]

### 순서와 FK
- 선택된 테이블 간 FK 의존성으로 정렬: 생성·적재는 부모 → 자식
- truncate 대상은 하나의 `TRUNCATE t1, t2, ...` 문으로 실행 (선택된 테이블 간 FK 허용)
- **선택하지 않은 테이블이 truncate 대상을 FK로 참조하면 미리보기 단계에서 오류** (CASCADE 사용 안 함)
- 자기참조 FK는 허용 (COPY 한 문장 끝에서 검사되므로 문제 없음). 서로 다른 테이블 간 FK 순환은 v1에서 오류 [내 결정]
- create 모드에서 선택 밖의 테이블을 참조하는 FK는, 타깃에 그 테이블이 있으면 생성하고 없으면 오류

### 컬럼 매칭 (truncate / append)
- 컬럼 이름으로 매칭
- 소스 컬럼이 타깃에 없거나 타입이 다르면 오류. 타입 비교는 `format_type` 결과 기준 정확히 일치. 예외로 타깃이 더 넓은 경우(`varchar(n)` 길이 ≥ 소스, `varchar`→`text`)는 허용. enum/domain은 이름으로 비교 [내 결정]
- 타깃에만 있는 컬럼은 DEFAULT로 채움 (NOT NULL에 DEFAULT 없으면 오류)
- generated 컬럼은 복사 대상 컬럼에서 제외

### 데이터 복사
- 소스 `COPY (SELECT cols FROM t) TO STDOUT` → 타깃 `COPY t (cols) FROM STDIN`을 스트림으로 직접 연결 (text 포맷, 중간 파일 없음)

### 시퀀스 [내 결정]
`setval`은 롤백되지 않으므로:
- create(새 테이블): 소스 값으로 `setval` (새로 만든 시퀀스라 롤백 시 시퀀스 자체가 사라짐)
- truncate / append: **시퀀스를 절대 낮추지 않음.** `max(소스 값, 타깃 현재 값)`으로 설정
- `setval`은 검증까지 끝난 뒤, 커밋 직전 마지막 단계에서 실행

### 검증
- 커밋 전 타깃 행 수를 확인: create/truncate는 소스 행 수와 같아야 하고, append는 (기존 행 수 + 소스 행 수)여야 함. 다르면 롤백
- 결과 화면에 테이블별 소스/타깃 행 수, 소요 시간 표시

## 7. 안전 장치 [내 결정]
- 접속 설정에 `env` 태그(dev / qa / prd 등). **타깃이 prd면 실행 전에 타깃 DB 이름을 직접 입력**해야 진행
- create의 "DROP 후 재생성", truncate가 포함된 실행은 미리보기에서 빨간색으로 강조
- 소스와 타깃이 같은 서버·포트·DB면 실행 거부
- **"DROP 후 재생성"은 타깃 env가 prd면 사용 불가.** 다른 env에서는 기존 테이블의 GRANT/OWNER/트리거/RLS가 사라진다는 경고 표시. DROP은 CASCADE 없이 실행하므로 뷰나 다른 테이블의 FK가 의존하면 미리보기에서 오류 [내 결정]
- truncate/append 시 타깃 테이블의 트리거는 끄지 않고 평소대로 실행됨. 트리거가 있으면 미리보기에 안내 표시 [내 결정]
- env 방향(dev→qa→prd)은 강제하지 않음 [내 결정]
- 미리보기에서 권한을 미리 확인: TRUNCATE/INSERT 권한, create 시 스키마 CREATE 권한, DROP 시 소유권, 시퀀스 UPDATE 권한 [내 결정]
- 실행마다 로그 파일 생성: 시각, 소스/타깃, 테이블별 모드, 실행한 DDL/SQL, 행 수, 결과. 위치는 exe 옆 `logs/`

## 8. 화면 흐름

1. **접속 선택**: 저장된 접속 목록에서 소스·타깃 선택 (env 태그 색상 표시). 비밀번호는 필요하면 입력
2. **테이블 선택**: 소스의 스키마/테이블 목록 (필터 검색, 체크박스, 테이블별 모드 지정, 소스 행 수 표시)
3. **미리보기**: 실행 순서, 테이블별 모드·소스 행 수·타깃 현재 행 수, 생성될 DDL(펼쳐보기), 경고/오류 목록. 오류가 있으면 실행 불가
4. **확인**: prd면 DB 이름 입력
5. **실행/진행**: 테이블별 진행 상태
6. **결과**: 성공/실패, 검증 결과, 로그 파일 경로

## 9. 접속 설정 [내 결정]
- 설정 파일: exe 옆 `pgmigrate.yaml` (없으면 `%APPDATA%\pgmigrate\pgmigrate.yaml`)
- 항목: name, env, host, port, database, user, sslmode
- **비밀번호는 저장하지 않음**: 실행 시 입력, 또는 `PGPASSWORD`, `.pgpass`(`%APPDATA%\postgresql\pgpass.conf`) 사용
- v1에서 접속 추가/수정은 설정 파일을 직접 편집 (TUI 편집 화면은 비목표)
- 대상 스키마: 소스와 같은 스키마 이름. 타깃에 스키마가 없으면 오류

## 10. 빌더에게 맡기는 것
- 패키지 구조, 내부 API, 라이브러리 세부 선택 (YAML 파서 등)
- 화면 세부 레이아웃과 키 바인딩
- 테스트 방식. 단, Docker의 PostgreSQL 18.4 두 인스턴스를 띄워 모드별 통합 테스트를 하는 것을 권장 (개발 PC에 Docker 있음)
