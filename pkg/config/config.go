package config

import (
	"fmt"
	"os"
	"strconv"

	"github.com/joho/godotenv"
)

type Config struct {
	// PostgreSQL (dormant when SQLitePath is set)
	DBURL string
	// Redis (dormant when SQLitePath is set)
	RedisURL string
	// SQLitePath: set SQLITE_PATH to activate lightweight single-file mode (no PG/Redis needed)
	SQLitePath string

	SteamAPIKey    string
	SteamAppID     string
	JWTPrivKey     string
	ServerPort     string
	AppEnv         string
	TrustedProxies string // comma-separated CIDRs; empty = trust no proxies
	// DiagIntervalSecs > 0 enables periodic pool-stat + memory logging (for load tests).
	DiagIntervalSecs int
	// SkinsCSVPath: gacha 풀 마스터 데이터 CSV 경로. 기본 Data/skins.csv (repo 루트 기준).
	SkinsCSVPath string
}

func Load() (*Config, error) {
	_ = godotenv.Load()

	cfg := &Config{
		DBURL:       os.Getenv("DB_URL"),
		RedisURL:    os.Getenv("REDIS_URL"),
		SQLitePath:  os.Getenv("SQLITE_PATH"),
		SteamAPIKey: os.Getenv("STEAM_API_KEY"),
		SteamAppID:  os.Getenv("STEAM_APP_ID"),
		JWTPrivKey:  os.Getenv("JWT_PRIVATE_KEY"),
		ServerPort:       getEnvOrDefault("SERVER_PORT", "8080"),
		AppEnv:           getEnvOrDefault("APP_ENV", "development"),
		TrustedProxies:   os.Getenv("TRUSTED_PROXIES"),
		DiagIntervalSecs: getEnvInt("DIAG_INTERVAL_SECS", 0),
		SkinsCSVPath:     getEnvOrDefault("SKINS_CSV_PATH", "Data/skins.csv"),
	}

	if err := cfg.validate(); err != nil {
		return nil, err
	}
	return cfg, nil
}

func (c *Config) validate() error {
	required := []struct {
		name  string
		value string
	}{
		{"JWT_PRIVATE_KEY", c.JWTPrivKey},
	}
	// DB_URL and REDIS_URL are only required in PostgreSQL+Redis mode.
	if c.SQLitePath == "" {
		required = append(required,
			struct{ name, value string }{"DB_URL", c.DBURL},
			struct{ name, value string }{"REDIS_URL", c.RedisURL},
		)
	}
	for _, r := range required {
		if r.value == "" {
			return fmt.Errorf("required environment variable %s is not set", r.name)
		}
	}
	if c.AppEnv == "production" {
		if c.SteamAPIKey == "" {
			return fmt.Errorf("required environment variable STEAM_API_KEY is not set in production")
		}
		if c.SteamAppID == "" {
			return fmt.Errorf("required environment variable STEAM_APP_ID is not set in production")
		}
	}
	return nil
}

func getEnvOrDefault(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

func getEnvInt(key string, def int) int {
	if v := os.Getenv(key); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			return n
		}
	}
	return def
}
