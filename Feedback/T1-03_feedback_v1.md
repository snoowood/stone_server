# T1-03 Feedback v1

- Reviewed at: `2026-05-03`
- Task: [T1-03.md](/F:/stone_server/Task/T1-03.md)
- Scope: Docker Compose configuration review

## Verdict

보류. `docker compose up -d --build`로 4개 서비스가 실제로 올라오는 것까지는 확인했지만, 현재 Nginx가 `443` 포트를 열어두고도 해당 포트에서 TLS 프록시를 받지 않아 태스크 목표를 완전히 충족했다고 보기 어렵습니다.

## Findings

1. Major: `443` is published, but Nginx does not listen on `443 ssl`.
   - Compose는 `443:443` 포트를 공개하지만 [docker-compose.yml](/F:/stone_server/docker-compose.yml:52), 실제 Nginx 설정에서 활성화된 서버 블록은 `listen 80`뿐입니다 [default.conf](/F:/stone_server/nginx/conf.d/default.conf:5).
   - 실검증에서도 `http://127.0.0.1/`는 404로 프록시 응답을 받았지만, `https://127.0.0.1/`는 TLS handshake 실패로 끝났습니다.
   - 태스크 설명이 `nginx (:443 → app:8080, SSL 설정 분리)`를 요구하는 만큼, 지금 상태는 443 엔드포인트가 선언만 되고 실제 동작하지 않는 상태입니다.

## Checklist Review

- `docker compose up` 시 4개 서비스 모두 정상 실행: 통과
- `postgres` 볼륨 재시작 후 데이터 보존: 통과
- `app` / `postgres` / `redis` 네트워크 통신 기반 구성: 통과
  - Compose 네트워크 생성, `postgres` / `redis` healthcheck 정상, 내부 네트워크에서 `pg_isready`와 `redis-cli ping` 모두 성공
- `nginx` → `app:8080` 프록시: 부분 통과
  - `http://127.0.0.1/` 요청은 Nginx를 거쳐 app의 404 응답으로 전달됨
  - `443` 프록시는 미동작

## Evidence

- `docker compose -f F:\stone_server\docker-compose.yml config`: 통과
- `docker compose -f F:\stone_server\docker-compose.yml up -d --build`: 통과
- `docker compose ps`: `app`, `postgres`, `redis`, `nginx` 모두 기동 확인
- Volume persistence test:
  - Postgres에 임시 테이블/레코드 생성
  - `docker compose restart postgres` 후 조회 결과 `1`
  - 이후 테스트 테이블 삭제
- Proxy test:
  - `http://127.0.0.1/` → `404`
  - `https://127.0.0.1/` → TLS handshake failed

## Notes

- 현재 app은 아직 DB/Redis를 실제로 사용하는 핸들러가 없어서, 애플리케이션 레벨 연결 성공보다는 Compose 네트워크와 서비스 접근성 기준으로 검증했습니다.
- 이번 검증을 위해 띄운 Compose 서비스는 리뷰 종료 후 `docker compose down`으로 정리했습니다. 볼륨은 삭제하지 않았습니다.
