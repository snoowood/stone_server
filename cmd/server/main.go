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
	"github.com/gensdeis/stone-server/internal/gacha"
	"github.com/gensdeis/stone-server/internal/health"
	"github.com/gensdeis/stone-server/internal/middleware"
	"github.com/gensdeis/stone-server/internal/player"
	"github.com/gensdeis/stone-server/internal/timeguard"
	"github.com/gensdeis/stone-server/pkg/cache"
	"github.com/gensdeis/stone-server/pkg/config"
	"github.com/gensdeis/stone-server/pkg/db"
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

	logger.Init(cfg.AppEnv)

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	var (
		sdb store.DB
		kv  kvstore.KVStore
	)

	if cfg.SQLitePath != "" {
		// ── SQLite mode (lightweight, no external services required) ──────────
		rawDB, err := sqlitedb.New(cfg.SQLitePath)
		if err != nil {
			log.Fatal().Err(err).Msg("sqlite init failed")
		}
		defer rawDB.Close()
		sdb = store.NewSQLAdapter(rawDB)
		kv = kvstore.NewMemStore()
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
	authHandler := auth.NewHandler(sdb, kv, steamClient, privKey)
	playerHandler := player.NewHandler(sdb, kv)
	gachaHandler := gacha.NewHandler(sdb, kv)
	steamAchClient := achievement.NewSteamAchievementClientForEnv(cfg.AppEnv, cfg.SteamAPIKey, cfg.SteamAppID)
	achievementHandler := achievement.NewHandler(sdb, kv, steamAchClient)

	var wg sync.WaitGroup
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
	}
	r.Use(middleware.RequestLogger())
	r.Use(gin.Recovery())

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
	secured.POST("/player/clicks", playerHandler.PostClicks)
	secured.POST("/gacha/pull", gachaHandler.Pull)
	secured.GET("/gacha/status", gachaHandler.Status)
	secured.GET("/gacha/logs", gachaHandler.Logs)
	secured.POST("/achievement/unlock", achievementHandler.Unlock)
	secured.GET("/achievement/list", achievementHandler.List)

	if cfg.AppEnv != "production" {
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
	log.Info().Msg("shutdown signal received, draining workers")
	wg.Wait()
	log.Info().Msg("workers drained, shutting down server")

	shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := srv.Shutdown(shutdownCtx); err != nil {
		log.Error().Err(err).Msg("server shutdown error")
	}
}
