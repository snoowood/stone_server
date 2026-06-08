# 공통 게임 설정 XML 스키마 명세 (game-config.xml)

- 작성일: 2026-05-31
- 관련 작업: 주간 계획서 T3-S1 ([weekly-plan-2026-05-31.md](weekly-plan-2026-05-31.md))
- 상태: 초안 (Codex 리뷰 대상)

---

## 1. 목적

클라이언트(`stone_project`)와 서버(`stone_server`)가 **반드시 동일해야 하는 게임 밸런스 값**을 단일 소스로 관리하기 위한 XML. 현재 양쪽에 하드코딩되어 드리프트 위험이 있는 5개 값을 공통화한다.

## 2. 위치 · 동기화

| 역할 | 경로 |
|---|---|
| **원본 (단일 소스)** | `stone_project/Assets/Resources/game-config.xml` |
| **동기화 사본** | `stone_server/Data/game-config.xml` |

- 원본은 **`stone_project`** 뿐이다. `stone_server` 사본은 직접 수정하지 않는다.
- **동기화 (T3-S4, skins.csv 와 동일 패턴)**: `stone_project/scripts/sync-game-config-to-server.sh` 가 post-commit 훅에서 원본 → 사본 복사 + stone_server 에 `chore(data): sync game-config.xml ...` 자동 커밋. (훅은 비버전관리 로컬 설정 — `.git/hooks/post-commit` 에 등록.)
- **CI 드리프트 검사 (R2)**: `stone_project/.github/workflows/game-config-sync-check.yml` 가 원본 변경 PR/푸시에서 public `stone_server` 를 체크아웃해 동일 스크립트의 `--check` 모드(CR 무시 내용 비교)로 일치 검증, 불일치 시 fail.
- **서버 사본 유효성**: `pkg/gameconfig` 의 `TestLoad_CommittedFileIsValid` 가 커밋된 `Data/game-config.xml` 의 파싱·검증을 보장(skins 의 `TestRealSkinsCSV_NoSkips` 대응).
- 클라이언트는 `Resources.Load<TextAsset>("game-config")`로, 서버는 `Data/game-config.xml`을 부팅 시 1회 로드 후 캐싱.

## 3. 루트 · 버전

```xml
<gameConfig version="1"> ... </gameConfig>
```

- `version` (정수, 루트 속성): 스키마 버전. 키 추가/삭제/의미 변경 시 증가.
- **불일치 정책 (R3, 경고 후 진행)**: 클라/서버 코드가 기대하는 `version`과 파일의 `version`이 다르면 **경고 로그만 남기고 부팅을 계속**한다. 부팅 거부/폴백 아님. CI 해시 검사가 1차 방어선이고, 런타임 경고는 보조 안전망.
- **(Codex) 경고 진행이 안전한 범위 = additive 변경뿐**: 키 추가 같은 호환 변경은 경고 후 진행해도 구버전이 무시하면 그만이다. 그러나 **키 삭제/이름 변경(destructive)** 은 구버전 코드가 해당 키를 누락으로 읽어 zero-value로 오해석할 수 있다 → 이 경우는 §6의 **누락 키 fail-fast** 로 자동 차단된다(경고 진행 아님). 즉 version은 운영 신호용이고, 실제 안전 보장은 §6 검증이 담당.

## 4. 키 정의 (1차 대상 5개)

도메인별로 그룹핑한다. 그룹은 서버 패키지 경계(`gacha`, `cairn`)와 일치.

> **(Codex 정정)** 아래 "서버/클라 적용 대상"은 **현재 하드코딩된 동일 값의 위치**이며, XML 로더는 아직 없다(작업 3-S2/S3에서 신설하며 이 위치들을 XML 주입으로 전환). 즉 "현재 코드와 같은 목표값"이지 "이미 로딩되는 경로"가 아니다.

| 그룹 | 엘리먼트 | 타입 | 단위 | 값 | 서버 적용 대상(현재 하드코딩) | 클라 적용 대상 |
|---|---|---|---|---|---|---|
| `gacha` | `pullCost` | float | enlightenment 포인트 | `100` | `gacha.DefaultConfig.PullCost` ([rng.go:46](../../internal/gacha/rng.go)) | `GameConfig.wishCairnCost` |
| `gacha` | `cooldownSeconds` | int | 초 | `1800` | `cooldownTTL = Duration(x)*time.Second` ([gacha.go:22](../../internal/gacha/gacha.go)) | `wishCairnCooldownMinutes = x/60` |
| `cairn` | `slotCount` | int | 개 | `5` | `cairn.SlotCount` ([cairn.go:22](../../internal/cairn/cairn.go)) | `wishCairnSlotCount` |
| `cairn` | `maxLayers` | int | 개 | `5` | `cairn.MaxLayers` ([cairn.go:23](../../internal/cairn/cairn.go)) | `wishCairnMaxLayerCount` |
| `cairn` | `spawnIntervalSeconds` | int | 초 | `30` | `cairn.SpawnIntervalSeconds` ([cairn.go:24](../../internal/cairn/cairn.go)) | `wishCairnStoneSpawnIntervalSeconds` |

**단위 원칙**: 모든 시간값은 **초(seconds)** 로 통일하고 엘리먼트 이름에 `Seconds` 접미사로 명시한다. 클라의 분 단위 필드(`wishCairnCooldownMinutes`)는 로더에서 `/60` 변환.

**범위 제외(클라 전용)**: `pointsPerInput`, `wishCairnCollectCost`(표시용), `eventMinInterval`/`eventMaxInterval`, `maxTimeStones`(서버 DB 제약으로 이미 일치). Enlightenment `rate`는 서버 DB 컬럼이 권위이므로 1차 제외(계획서 R4).

### 4.1 vow 그룹 (발원 / Vow crafting) — 추가됨

| 그룹 | 엘리먼트 | 타입 | 단위 | 값 | 비고 |
|---|---|---|---|---|---|
| `vow` | `requiredItemCount` | int | 개 | `7` | Vow 1회에 필요한 skin 아이템 수 |
| `vow` | `tiers/tier` | 반복 | — | 4개 | `targetRarity` 속성으로 보상 등급 지정 |
| `tier` | `minNPower` / `maxNPower` | float | N power | 등급별 | 해당 등급이 받는 N power 범위 |
| `tier` | `minSuccessRate` / `maxSuccessRate` | float | % (0..100) | 등급별 | min/max N power 지점의 성공률 |

- `tier` 는 `targetRarity` ∈ {`Uncommon`,`Rare`,`Unique`,`Legendary`} (대소문자 무시, `Common` 제외). 중복 금지.
- **현재 서버 소비처 없음**: 서버는 vow 도메인 로직이 아직 없으나, 공통 스키마 검증 패리티(클라 `GameConfigLoader.ApplyVowXmlOverrides` 와 동일 규칙)를 위해 `pkg/gameconfig` 가 파싱·검증한다. 키 추가는 additive 라 `version` 은 `1` 유지(클라 `ExpectedVersion` 도 1).

## 5. 엘리먼트 네이밍 규칙

- **camelCase** 엘리먼트 이름. 양쪽 파서에서 명시적 매핑(Go struct tag / C# XmlElement)으로 바인딩.
- 그룹 엘리먼트(`gacha`, `cairn`) 하위에 값 엘리먼트.
- **(Codex P3) JSON wire 네이밍과의 차이는 의도적**: 서버 JSON 응답은 snake_case(`slot_count`, `spawn_interval_sec` 등)지만, 본 XML은 **내부 설정 파일**로 JSON wire 계약과 별개다. 명시적 매핑 태그가 있으므로 네이밍 통일 강제는 불필요 — 각 언어 관례(C# camelCase)에 맞춘다. 단 키 의미는 §4 표가 단일 출처.

## 6. 검증 규칙 (로더 fail-fast)

부팅 시 로더가 검증, 위반 시 **부팅 실패**(version 불일치와 달리 값 자체 오류는 fail-fast):

**존재 검증 (Codex P1 — 누락 ≠ 0 구분)**
- 값 타입 struct는 누락 시 zero-value로 조용히 통과한다. → 파서 struct는 **포인터 필드(`*int`, `*float64`)** 로 받아 `nil` 이면 "누락"으로 판정, 명시적으로 reject. (§7 예시 참조)
- 모든 키(`version` 포함) 존재 필수. 하나라도 누락 시 부팅 실패.

**값 범위 검증**
- `pullCost` 는 finite (NaN/Inf 거부 — `<= 0` 비교를 통과하므로 별도 검사). 그 뒤 `pullCost > 0`
- `cooldownSeconds > 0` (0 미허용 — 누락 검출 강화 겸. dev용 0 쿨다운은 본 스키마 범위 밖)
- `cooldownSeconds % 60 == 0` (Codex P2 — 클라가 분으로 변환 시 절삭 방지)
- `slotCount > 0`
- `maxLayers > 0`
- `spawnIntervalSeconds > 0`

**파생값/정합성 검증 (Codex P1 — 정수 나눗셈)**
- `PhaseOffset() = spawnIntervalSeconds * maxLayers / slotCount` 는 **정수 나눗셈**이다 ([cairn.go:29](../../internal/cairn/cairn.go)). `> 0` 만으로는 부족 — 나누어떨어지지 않으면 소수부가 버려져 슬롯 위상이 조용히 앞당겨진다.
  → `(spawnIntervalSeconds * maxLayers) % slotCount == 0` (정확 분할) 강제. 현재값 `30*5/5=30` 은 충족.

**상한 검증 (Codex P3 — 극단값 방어)**
- `slotCount <= 50`, `maxLayers <= 100`, `spawnIntervalSeconds <= 86400`(1일), `cooldownSeconds <= 604800`(1주)
- 이유: `slotCount`/`maxLayers` 과대값은 `InitializeSlots` 반복·`/player/state` 응답 크기를 비정상적으로 키운다 ([cairn.go:74](../../internal/cairn/cairn.go), [player.go:181](../../internal/player/player.go)). 상한은 운영 가드레일(필요 시 조정).

**vow 검증 (서버 fail-fast, 클라는 폴백 — 규칙 동일)**
- `requiredItemCount` 존재 필수 + `> 0`
- `tiers` 에 `tier` 1개 이상
- 각 `tier`: `targetRarity` ∈ {Uncommon,Rare,Unique,Legendary}(Common 제외), 중복 금지
- `minNPower`/`maxNPower`/`minSuccessRate`/`maxSuccessRate` 모두 존재 + finite(NaN/Inf 거부)
- `minNPower >= 0`, `maxNPower > minNPower`
- `minSuccessRate`/`maxSuccessRate` ∈ 0..100, `maxSuccessRate >= minSuccessRate`

## 7. 파서 매핑 예시

### 서버 (Go, `encoding/xml`)

포인터 필드로 받아 `nil`(누락)과 `0`(명시적 0)을 구분한다. 파싱 후 모든 필드 non-nil 검사 → 범위 검증 → non-pointer 캐시 struct로 변환.

```go
// 1) 파싱용 (포인터 = 존재 검출)
type gameConfigXML struct {
    XMLName xml.Name `xml:"gameConfig"`
    Version *int     `xml:"version,attr"`
    Gacha   struct {
        PullCost        *float64 `xml:"pullCost"`
        CooldownSeconds *int     `xml:"cooldownSeconds"`
    } `xml:"gacha"`
    Cairn struct {
        SlotCount            *int `xml:"slotCount"`
        MaxLayers            *int `xml:"maxLayers"`
        SpawnIntervalSeconds *int `xml:"spawnIntervalSeconds"`
    } `xml:"cairn"`
    Vow struct {
        RequiredItemCount *int `xml:"requiredItemCount"`
        Tiers struct {
            Tier []struct {
                TargetRarity   string   `xml:"targetRarity,attr"`
                MinNPower      *float64 `xml:"minNPower"`
                MaxNPower      *float64 `xml:"maxNPower"`
                MinSuccessRate *float64 `xml:"minSuccessRate"`
                MaxSuccessRate *float64 `xml:"maxSuccessRate"`
            } `xml:"tier"`
        } `xml:"tiers"`
    } `xml:"vow"`
}

// 2) 캐시용 (검증 통과 후 확정값)
type GameConfig struct {
    Version              int
    PullCost             float64
    CooldownSeconds      int
    SlotCount            int
    MaxLayers            int
    SpawnIntervalSeconds int
}
// Load: Unmarshal → 전 필드 nil 검사(누락 reject) → §6 범위/정합성 검증 → GameConfig 반환(fail-fast)
```

### 클라이언트 (C#, `System.Xml.Serialization`)

```csharp
[XmlRoot("gameConfig")]
public class GameConfigXml {
    [XmlAttribute("version")] public int Version;
    [XmlElement("gacha")]     public GachaCfg Gacha;
    [XmlElement("cairn")]     public CairnCfg Cairn;
}
public class GachaCfg {
    [XmlElement("pullCost")]        public float PullCost;
    [XmlElement("cooldownSeconds")] public int   CooldownSeconds;
}
public class CairnCfg {
    [XmlElement("slotCount")]            public int SlotCount;
    [XmlElement("maxLayers")]            public int MaxLayers;
    [XmlElement("spawnIntervalSeconds")] public int SpawnIntervalSeconds;
}
```

> C#도 값 타입은 누락 시 0이 되어 동일한 검출 갭이 있다. nullable(`int?`, `float?`)로 받거나, 역직렬화 후 원본 XML에 해당 노드 존재 여부를 명시 검사할 것. 클라 로더도 §6 검증을 동일 적용(누락/범위 위반 시 경고 후 기존 기본값 폴백 — 클라는 부팅 거부 대신 폴백, 계획서 T3-S2).

## 8. 변경 프로세스

1. `stone_project`의 원본 XML 수정 (값 변경 시 `version`은 유지, 키 구조 변경 시 `version` 증가)
2. 동기화 스크립트 실행 → `stone_server/Data/game-config.xml` 갱신
3. CI 해시 검사 통과 확인
4. 키 구조 변경 시 양쪽 파서 struct + 본 명세 갱신

### 8.1 cairn 차원 변경 시 기존 저장 데이터 정책 (T3-S3b)

`slotCount` / `maxLayers` / `spawnIntervalSeconds` 는 신규 player 슬롯 생성(`cairn.InitializeSlots`)에 즉시 반영된다. **기존 player 의 저장된 슬롯 row** 는 다음 정책을 따른다 — 자동 처리되는 부분과 수동 마이그레이션이 필요한 부분을 구분할 것:

| 변경 | SQLite 모드 (부팅 시 `sqlitedb.backfillCairnSlots`) | PostgreSQL 모드 |
|---|---|---|
| slotCount **증가** | 부족한 index 자동 보강(`INSERT OR IGNORE`) + 런타임 lazy-init([player.go](../../internal/player/player.go)) | 런타임 lazy-init 으로 자동 보강 |
| slotCount **감소** | `slot_index >= slotCount` 자동 DELETE (부팅 idempotent) | **hand-written 마이그레이션 필요** (자동 정리 없음) |
| `spawnInterval`/`maxLayers` 변경 (phase 재정렬) | **자동 안 함** — 기존 row 의 `started_at` 유지 | **자동 안 함** — 기존 row 의 `started_at` 유지 |

- **count-only reconcile**: 두 모드 모두 슬롯 **개수**만 맞추고, 기존 슬롯의 `started_at`(=phase stagger)은 재작성하지 않는다. `started_at` 재정렬은 플레이어 진행도를 바꾸는 결정이므로 자동화하지 않는다.
- 기존 플레이어까지 새 phase 로 맞추거나 PG 에서 슬롯을 줄이려면 **의도적 ops 마이그레이션**으로 처리한다(계획서 R5). 이 비대칭(SQLite 부팅 정리 ↔ PG 마이그레이션)은 각 모드의 스키마 관리 방식(SQLite 는 인라인 reconcile, PG 는 migrations/)을 그대로 따른 것이다.

## 9. 미결 (Q2)

- 클라 로드 키 이름: `Resources.Load<TextAsset>("game-config")` — 파일명 `game-config.xml` 확정 제안. (Resources 로드는 확장자 제외 → `"game-config"`)
- `.meta` 파일은 Unity 임포트 시 자동 생성.
