# 02_database Feedback

- 검토 버전: `v1.0`
- 검토 일시: `2026-05-03`
- 원본 문서: [02_database.md](/F:/stone_server/Goal/02_database.md)
- 검증 범위: 스키마 정합성, 문서 간 일치 여부, 운영 리스크 점검

## 총평

초기 서비스용 스키마로는 충분히 깔끔하고, 주요 도메인 테이블 분리도 적절하다. 다만 가챠 보상 모델, 세션 모델, 재시도 큐 모델은 지금 단계에서 조금 더 못 박아야 이후 마이그레이션 비용이 줄어든다.

## 확인된 강점

- `players`, `player_states`, `inventories`, `gacha_logs`, `player_achievements` 분리가 명확하다.
- 가챠 로그와 업적 상태를 별도 테이블로 관리하는 방향은 감사와 CS 대응에 유리하다.
- Redis 키 패턴도 인증, 쿨다운, rate limit 기준으로 잘 나뉘어 있다.

## 주요 피드백

1. `inventories UNIQUE (player_id, item_id)`는 중복 아이템 드랍 정책을 강하게 제한한다.
가챠에서 같은 스킨이 다시 나올 수 있다면 현재 스키마는 저장이 불가능하다. 중복을 막을지, `quantity`를 둘지, 중복 시 포인트 환급으로 바꿀지 문서에 명시가 필요하다.

2. 세션/리프레시 키를 `player_id` 단위로만 두면 다중 기기 로그인이 불편해질 수 있다.
`session:{player_id}`와 `refresh:{player_id}` 구조는 새 로그인 시 이전 세션을 덮어쓸 가능성이 높다. 여러 기기를 허용할 계획이면 `session_id` 또는 `jti` 기준 키 설계가 더 안전하다.

3. 업적 재시도 큐가 Redis와 PostgreSQL에 이중 정의되어 있다.
Redis `ach:retry`와 `achievement_retry_queue` 테이블 중 어느 쪽이 실제 source of truth인지 정해야 한다. 둘 다 운용하면 일관성 관리 비용이 생긴다.

4. `server_seed` 원문 저장은 보안/악용 관점에서 재검토가 필요하다.
감사용 목적이라면 재현 가능한 원문보다 해시 또는 감사용 별도 식별자 저장이 더 안전할 수 있다. 특히 시드 재사용 가능성이 생기면 확률 추론 리스크가 생긴다.

5. 조회 성능용 보조 인덱스가 조금 더 필요할 수 있다.
`inventories(player_id)`, `player_achievements(player_id)`, `achievement_retry_queue(next_retry_at)` 같은 인덱스는 실제 API 구현 시 바로 필요해질 가능성이 높다.

6. 로그인 관련 필드 역할이 겹친다.
`players.last_login`과 `player_states.last_login_date`가 함께 있는데, 하나는 실제 로그인 시각, 하나는 스트릭 판정용 UTC 날짜라고 문서에 더 분명히 적어두면 혼동이 줄어든다.

## 권장 수정

- 중복 아이템 정책을 문서화하고 `inventories` 스키마에 반영
- 세션 키를 `player_id` 단일 키에서 세션 식별자 기반으로 확장 검토
- 업적 재시도 큐는 Redis 또는 PostgreSQL 한 곳을 주 저장소로 확정
- 가챠 감사용 시드 저장 형식 재검토

---

## 2차 검토 (`v2.0`, 2026-05-03)

### 반영 확인

- 중복 아이템 정책이 "인벤토리 미저장 + 포인트 환급"으로 확정되었다.
- 세션 키가 `session:{jti}` 기반으로 바뀌어 다중 세션 확장성이 좋아졌다.
- 업적 재시도는 Redis 주 큐, PostgreSQL 감사 기록으로 책임이 분리되었다.
- 가챠 감사용 값이 `gacha_seed_hash`로 변경되어 원문 시드 저장 리스크가 줄었다.
- 보조 인덱스들이 추가되어 실제 API 조회 패턴과 더 잘 맞아졌다.

### 남은 보완 사항

1. Refresh Token 키는 아직 `refresh:{player_id}`라 세션 모델과 완전히 대칭적이지 않다.
세션은 `jti` 단위인데 refresh token은 플레이어 단위라, 다중 기기 허용 시 새 로그인 또는 토큰 재발급 정책이 애매해질 수 있다. `refresh:{token_id}` 또는 `refresh:{jti}` 계열 설계를 검토할 만하다.

2. 클릭 이벤트 검증에 필요한 저장 모델은 아직 없다.
`POST /player/clicks`를 핵심 포인트 수급 경로로 둘 계획이라면, 중복 제출 방지용 배치 ID, 마지막 처리 시각, 또는 anti-replay 메타데이터를 둘지 미리 정해두는 편이 좋다.

### 2차 판정

DB 문서는 **구현 착수 가능 수준**이다. 다만 refresh token 다중 세션 정책만 한 번 더 결정해두면 후속 인증 구현이 훨씬 깔끔해진다.

---

## 3차 검토 (`v3.0`, 2026-05-03)

### 반영 확인

- `session:current:{player_id}` 보조 키가 추가되어 단일 세션 강제 로직이 구체화되었다.
- `refresh:{player_id}`가 단일 세션 정책과 연결되어 설명되었다.
- `last_click_batch_id`가 `player_states`에 추가되어 클릭 anti-replay 보조 수단이 생겼다.

### 남은 보완 사항

1. `last_click_batch_id` 하나만으로는 지연 재전송을 충분히 막기 어렵다.
가장 최근 배치 하나만 저장하면 더 오래된 `batch_id` 재전송은 잡지 못한다. Redis TTL 60초와 조합하더라도 보호 창이 짧으므로, 최근 배치 히스토리를 별도 저장하거나 클라이언트 sequence 번호를 두는 쪽이 더 견고하다.

2. 로그아웃/재로그인 경합을 고려한 키 삭제 규칙이 DB/캐시 설계 주석에 있으면 좋다.
현재 키 구성은 괜찮지만, `session:current:{player_id}` 값이 요청 JWT의 jti와 일치할 때만 current session/refesh를 지우는 규칙을 적어두면 구현 오해를 줄일 수 있다.

### 3차 판정

DB 설계는 **거의 확정 가능**하다. 다만 클릭 배치 이력 보존 전략만 조금 더 견고하게 정하면 좋다.

---

## 4차 검토 (`v4.0`, 2026-05-03)

### 반영 확인

- `last_click_batch_id` 컬럼과 007 마이그레이션이 제거되어 DB가 불필요한 anti-replay 상태를 떠안지 않게 되었다.
- Redis 키 설계가 `click:batch:{player_id}:{batch_id}` TTL 300s 기준으로 단순화되었다.
- 로그아웃 compare-and-delete 규칙이 캐시 설계 섹션에 반영되었다.

### 남은 보완 사항

1. refresh 시 세션 키 전환 규칙이 DB/캐시 설계에 추가되면 더 좋다.
현재는 새 로그인 시 강제 단일 세션 로직과 로그아웃 경합 규칙은 있지만, `POST /auth/refresh`가 새 JWT를 발급할 때 old/new jti를 어떻게 바꾸는지 캐시 설계 관점의 명시가 없다.

2. 5분 TTL 기반 anti-replay는 저장소 설계상 의도적 한계가 있다.
현재 구조는 최근 5분 범위 재전송만 막는다. 이것이 허용 가능한 제품 결정이라면 괜찮지만, "장시간 지연 패킷에 대한 중복 적립은 방지 범위 밖"이라는 점을 문서상 명확히 받아들이는 편이 좋다.

### 4차 판정

DB/캐시 설계는 **거의 확정 완료**다. 남은 것은 refresh 시 old/new jti 전환 규칙 명시 정도다.

---

## 5차 검토 (`v5.0`, 2026-05-03)

### 반영 확인

- `POST /auth/refresh` 시 `old_jti` 삭제와 `new_jti` 등록 규칙이 캐시 설계에 추가되었다.
- 클릭 anti-replay가 DB 컬럼 없이 Redis TTL 300초 정책으로 일관되게 정리되었다.
- 로그아웃 compare-and-delete와 refresh jti 전환이 같은 세션 모델 안에서 모순 없이 맞물린다.

### 남은 보완 사항

1. 현재 문서 범위에서 치명적 스키마 누락은 보이지 않는다.
추가 논의가 필요하다면 refresh token rotation 도입 시 키 구조를 어떻게 바꿀지 정도인데, 이는 현행 설계의 결함은 아니다.

### 5차 판정

DB/캐시 설계는 **최종 확정 가능 수준**이다.
