# T1-01 Feedback v1

- Reviewed at: `2026-05-03`
- Task: [T1-01.md](/F:/stone_server/Task/T1-01.md)
- Related goal: [00_overview.md](/F:/stone_server/Goal/00_overview.md)
- Scope: Go project bootstrap review

## Verdict

보류. 기본 골격 생성과 `go build ./...` 검증은 통과했지만, 현재 `go.mod`와 `Dockerfile`의 Go 버전이 맞지 않아 Docker 기반 빌드 성공 조건을 충족했다고 보기 어렵습니다.

## Findings

1. Major: Docker builder Go version does not satisfy the module Go version.
   - `go.mod`는 `go 1.26.1`을 요구하지만 [go.mod](/F:/stone_server/go.mod:3), `Dockerfile` 빌더 이미지는 `golang:1.22-alpine`을 사용하고 있습니다 [Dockerfile](/F:/stone_server/Dockerfile:1).
   - 로컬 `go build ./...`는 통과했지만, Docker 빌드 환경은 더 낮은 Go 버전이라 실제 컨테이너 빌드가 실패하거나 이후 문법/표준 라이브러리 사용 시 바로 깨질 가능성이 큽니다.

## Checklist Review

- `go build ./...`: 통과
- `00_overview.md` 기준 디렉터리 구조 생성: 통과
- Dockerfile 빌드 성공: 미검증
  - 이 환경에서는 Docker daemon이 실행 중이지 않아 실제 `docker build` 완료 여부를 끝까지 확인하지 못했습니다.
  - 다만 정적 검토상 최종 이미지에는 바이너리만 복사하므로 불필요한 소스 파일이 최종 런타임 이미지에 포함되지는 않습니다 [Dockerfile](/F:/stone_server/Dockerfile:17).

## Evidence

- Local Go version: `go1.26.1 windows/amd64`
- Docker build attempt: Docker daemon unavailable (`//./pipe/dockerDesktopLinuxEngine` not found)
- Main entrypoint exists: [main.go](/F:/stone_server/cmd/server/main.go:1)

## Recommended Fix

둘 중 하나로 맞추는 것이 좋습니다.

1. `Dockerfile` 빌더 이미지를 `go.mod`와 같은 Go 버전 계열로 올리기
2. 실제 목표 버전이 1.22라면 `go.mod`의 Go 버전을 1.22 계열로 낮추기

수정 후에는 Docker daemon이 켜진 환경에서 `docker build`를 다시 확인해야 T1-01을 완료로 올릴 수 있습니다.
