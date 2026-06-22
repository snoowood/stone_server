package main

import (
	"context"
	"crypto/rsa"
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"runtime"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/rs/zerolog/log"

	"github.com/gensdeis/stone-server/internal/achievement"
	"github.com/gensdeis/stone-server/internal/auth"
	"github.com/gensdeis/stone-server/internal/cairn"
	"github.com/gensdeis/stone-server/internal/gacha"
	"github.com/gensdeis/stone-server/internal/health"
	"github.com/gensdeis/stone-server/internal/middleware"
	"github.com/gensdeis/stone-server/internal/player"
	"github.com/gensdeis/stone-server/internal/timeguard"
	"github.com/gensdeis/stone-server/internal/vow"
	"github.com/gensdeis/stone-server/pkg/cache"
	"github.com/gensdeis/stone-server/pkg/config"
	"github.com/gensdeis/stone-server/pkg/db"
	"github.com/gensdeis/stone-server/pkg/gameconfig"
	"github.com/gensdeis/stone-server/pkg/kvstore"
	"github.com/gensdeis/stone-server/pkg/logger"
	"github.com/gensdeis/stone-server/pkg/sqlitedb"
	"github.com/gensdeis/stone-server/pkg/store"
)

func main() {
	cfg, err := config.Load()
	if err != nil {
		fmt.Fprintf(os.Stderr, "configuration error: %v\n", err)
		os.Exit(1)
	}

	logger.Init(cfg.AppEnv, cfg.LogLevel)

	// SkinPool 은 DB 초기화/마이그레이션보다 먼저 로드·검증한다. 스킵 행이 있는 잘못된
	// CSV 는 아래 skinStats Fatal 가드가 부팅을 거부하므로, DB·마이그레이션에 손대기 전에
	// fail-fast 시킨다(라이브 드롭률이 조용히 바뀌는 것을 차단).
	skinPool, err := gacha.LoadSkinPool(cfg.SkinsCSVPath)
	if err != nil {
		log.Fatal().Err(err).Str("path", cfg.SkinsCSVPath).Msg("skin pool load failed")
	}
	// 스킵 행이 단 하나라도 있으면 무음 데이터 누락으로 라이브 드롭률이 바뀐다.
	// 부팅 단계에서 거부해 운영 풀이 잘못 로드되는 것을 차단.
	skinStats := skinPool.Stats()
	if skinStats.SkippedBadRarity+skinStats.SkippedBadPart+skinStats.SkippedMalformed > 0 {
		log.Fatal().
			Str("path", cfg.SkinsCSVPath).
			Int("bad_rarity", skinStats.SkippedBadRarity).
			Int("bad_part", skinStats.SkippedBadPart).
			Int("malformed", skinStats.SkippedMalformed).
			Int("loaded", skinStats.Loaded).
			Int("total", skinStats.Total).
			Msg("skin pool had skipped rows — refusing to start (silently truncated pool would change drop odds)")
	}
	log.Info().Int("loaded", skinStats.Loaded).Msg("skin pool ready")

	// 공통 game-config.xml 로드 (클라/서버 단일 소스). fail-fast on malformed/out-of-range.
	gameCfg, err := gameconfig.Load(cfg.GameConfigXMLPath)
	if err != nil {
		log.Fatal().Err(err).Str("path", cfg.GameConfigXMLPath).Msg("game config load failed")
	}
	if gameCfg.Version != gameconfig.ExpectedVersion {
		// R3: version 불일치는 경고 후 진행. 구조적 깨짐은 위 Load 의 누락 키 검증이 차단.
		log.Warn().
			Int("file_version", gameCfg.Version).
			Int("expected_version", gameconfig.ExpectedVersion).
			Msg("game config version mismatch — proceeding")
	}
	cairnCfg := cairn.Config{
		SlotCount:            gameCfg.SlotCount,
		MaxLayers:            gameCfg.MaxLayers,
		SpawnIntervalSeconds: gameCfg.SpawnIntervalSeconds,
	}
	gachaCfg := gacha.GameConfig{
		PullCost: gameCfg.PullCost,
		Cooldown: time.Duration(gameCfg.CooldownSeconds) * time.Second,
	}
	log.Info().Int("version", gameCfg.Version).Msg("game config ready")

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	var (
		sdb store.DB
		kv  kvstore.KVStore
	)

	if cfg.SQLitePath != "" {
		// ── SQLite mode (lightweight, no external services required) ──────────
		rawDB, err := sqlitedb.New(cfg.SQLitePath, cairnCfg.SlotCount, int(cairnCfg.PhaseOffset().Seconds()))
		if err != nil {
			log.Fatal().Err(err).Msg("sqlite init failed")
		}
		defer rawDB.Close()
		sdb = store.NewSQLAdapter(rawDB)
		mem := kvstore.NewMemStore()
		// XC-3: MemStore expires keys only lazily on access, so sweep periodically to
		// reclaim write-once keys (e.g. refresh_lookup:<token>) that are never re-read.
		mem.StartSweeper(ctx, 5*time.Minute)
		kv = mem
		log.Info().Str("path", cfg.SQLitePath).Msg("sqlite mode: db and session store ready")

	} else {
		// ── PostgreSQL + Redis mode (dormant unless SQLITE_PATH is unset) ─────
		log.Info().Msg("running db migrations")
		if err := db.RunMigrations(cfg.DBURL); err != nil {
			log.Fatal().Err(err).Msg("migration failed")
		}
		log.Info().Msg("db migrations complete")

		pool, err := db.NewPool(ctx, cfg.DBURL)
		if err != nil {
			log.Fatal().Err(err).Msg("db pool init failed")
		}
		defer pool.Close()
		sdb = store.NewPgxAdapter(pool)
		log.Info().Msg("db pool ready")

		// Periodic pool-stat + memory diagnostics (opt-in via DIAG_INTERVAL_SECS).
		if cfg.DiagIntervalSecs > 0 {
			interval := time.Duration(cfg.DiagIntervalSecs) * time.Second
			go func() {
				ticker := time.NewTicker(interval)
				defer ticker.Stop()
				for {
					select {
					case <-ctx.Done():
						return
					case <-ticker.C:
						runtime.GC()
						stat := pool.Stat()
						var mem runtime.MemStats
						runtime.ReadMemStats(&mem)
						log.Info().
							Int32("pool_total_conns", stat.TotalConns()).
							Int32("pool_idle_conns", stat.IdleConns()).
							Int32("pool_acquired_conns", stat.AcquiredConns()).
							Uint64("heap_alloc_bytes", mem.HeapAlloc).
							Uint64("heap_sys_bytes", mem.HeapSys).
							Uint32("num_gc", mem.NumGC).
							Msg("diag")
					}
				}
			}()
		}

		rdb, err := cache.NewClient(cfg.RedisURL)
		if err != nil {
			log.Fatal().Err(err).Msg("redis client init failed")
		}
		defer rdb.Close()
		kv = kvstore.NewRedisStore(rdb)
		log.Info().Msg("redis client ready")
	}

	privKey, err := auth.ParsePrivateKey(cfg.JWTPrivKey)
	if err != nil {
		log.Fatal().Err(err).Msg("parse jwt private key failed")
	}

	steamClient := auth.NewSteamClientForEnv(cfg.AppEnv, cfg.SteamAPIKey, cfg.SteamAppID)
	authHandler := auth.NewHandler(sdb, kv, steamClient, privKey, cairnCfg)
	playerHandler := player.NewHandler(sdb, kv, cairnCfg)

	gachaHandler := gacha.NewHandler(sdb, kv, skinPool, gachaCfg, cairnCfg)
	vowHandler := vow.NewHandler(sdb, skinPool, gameCfg.VowRequiredItemCount, gameCfg.VowTiers, cfg.AppEnv == "development")
	steamAchClient := achievement.NewSteamAchievementClientForEnv(cfg.AppEnv, cfg.SteamAPIKey, cfg.SteamAppID)
	achievementHandler := achievement.NewHandler(sdb, kv, steamAchClient)

	var wg sync.WaitGroup
	// LOG-3: rebuild the retry queue from achievement_retry_queue (the source of truth)
	// before the worker starts ticking — recovers the dev MemStore queue (wiped on
	// restart) and reconciles a surviving prod Redis queue without duplicating rows.
	if reloaded, err := achievement.ReloadPendingRetries(ctx, sdb, kv, achievement.DefaultMaxRetries); err != nil {
		log.Error().Err(err).Msg("achievement: reload pending retries failed")
	} else if reloaded > 0 {
		log.Info().Int("reloaded", reloaded).Msg("achievement: reloaded pending retries into queue")
	}
	achievement.NewWorker(sdb, kv, steamAchClient).Start(ctx, &wg)

	if cfg.AppEnv == "production" {
		gin.SetMode(gin.ReleaseMode)
	}

	r := gin.New()
	if cfg.TrustedProxies != "" {
		proxies := strings.Split(cfg.TrustedProxies, ",")
		r.SetTrustedProxies(proxies)
	} else {
		r.SetTrustedProxies(nil)
		// SEC-4: with no trusted proxies ClientIP is the direct peer — correct for a
		// directly-exposed server, but if this instance actually sits behind a proxy/LB
		// every request appears to come from the proxy IP and IP-based rate limits (e.g.
		// /auth/steam 10/min) collapse into one shared bucket. Warn in production so a
		// forgotten TRUSTED_PROXIES after introducing an LB is visible at boot.
		if cfg.AppEnv == "production" {
			log.Warn().Msg("TRUSTED_PROXIES is empty — IP-based rate limiting keys on the direct peer IP; set TRUSTED_PROXIES if this instance is behind a proxy/LB")
		}
	}
	r.Use(middleware.RequestLogger())
	r.Use(middleware.Recovery())

	pubKey := privKey.Public().(*rsa.PublicKey)

	v1 := r.Group("/api/v1")

	public := v1.Group("")
	public.Use(middleware.RateLimiter(kv))
	public.GET("/health", health.Handler(sdb, kv))
	public.POST("/auth/steam", authHandler.AuthSteam)
	public.POST("/auth/refresh", authHandler.AuthRefresh)
	public.POST("/time/sync", timeguard.Handler)

	logoutGrp := v1.Group("")
	logoutGrp.Use(middleware.JWTSignatureAuth(pubKey))
	logoutGrp.DELETE("/auth/logout", authHandler.AuthLogout)

	secured := v1.Group("")
	secured.Use(middleware.JWTAuth(pubKey, kv))
	secured.Use(middleware.RateLimiter(kv))
	secured.GET("/player/state", playerHandler.GetState)
	secured.POST("/player/sync", playerHandler.Sync)
	secured.POST("/gacha/pull", gachaHandler.Pull)
	secured.GET("/gacha/status", gachaHandler.Status)
	secured.GET("/gacha/logs", gachaHandler.Logs)
	secured.POST("/vow/pray", vowHandler.Pray)
	secured.POST("/achievement/unlock", achievementHandler.Unlock)
	secured.GET("/achievement/list", achievementHandler.List)

	if cfg.AppEnv == "development" {
		// 운영 오인지 방지: dev 엔드포인트(무비번 /auth/dev, 임의 steam_id /internal/dev-token)와
		// mock Steam 클라이언트가 활성화됨을 부팅 로그에 눈에 띄게 남긴다. APP_ENV 화이트리스트
		// (config.validate)가 비정규값 부팅을 차단하므로, dev 활성은 명시적 development 에서만 발생.
		log.Warn().
			Str("app_env", cfg.AppEnv).
			Msg("DEV ENDPOINTS ACTIVE — /auth/dev, /internal/dev-token, mock Steam enabled. MUST NOT run in production.")

		public.POST("/auth/dev", authHandler.AuthDev)

		internal := v1.Group("/internal")
		internal.GET("/dev-token", authHandler.DevToken)
	}

	srv := &http.Server{Addr: ":" + cfg.ServerPort, Handler: r}

	go func() {
		log.Info().Str("port", cfg.ServerPort).Msg("stone-server starting")
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Fatal().Err(err).Msg("server failed to start")
		}
	}()

	<-ctx.Done()
	log.Info().Msg("shutdown signal received, stopping HTTP server")

	// SRE-4: stop accepting new requests (and drain in-flight ones) BEFORE waiting
	// on background workers, so no new work is enqueued into the workers while we
	// drain them. The previous order (wg.Wait → srv.Shutdown) let live HTTP traffic
	// keep feeding the workers during the "drain" window.
	//
	// TODO(multi-instance): before srv.Shutdown, flip a /health "draining" flag so it
	// returns 503 and the load balancer deregisters this instance first, closing the
	// in-flight-rejection window. Single-instance today, so not yet wired.
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := srv.Shutdown(shutdownCtx); err != nil {
		log.Error().Err(err).Msg("server shutdown error")
	}
	log.Info().Msg("HTTP server stopped, draining workers")
	wg.Wait()
	log.Info().Msg("workers drained")
}
