FROM --platform=$BUILDPLATFORM golang:1.25-alpine AS builder

ARG TARGETOS
ARG TARGETARCH

WORKDIR /app

COPY go.mod go.sum* ./
RUN go mod download

COPY . .
RUN CGO_ENABLED=0 GOOS=${TARGETOS:-linux} GOARCH=${TARGETARCH:-amd64} \
    go build -ldflags="-s -w" -o /app/server ./cmd/server

FROM alpine:3.19

RUN addgroup -S app && adduser -S app -G app

WORKDIR /app

COPY --from=builder /app/server .
# 가챠 풀 마스터 데이터(런타임에 SKINS_CSV_PATH=Data/skins.csv 로 읽음).
COPY --from=builder /app/Data ./Data

USER app

EXPOSE 8080

ENTRYPOINT ["./server"]
