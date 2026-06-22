package config

import (
	"os"
	"testing"
)

// R2 (Load path): an unset or empty APP_ENV must fail config.Load() — not silently
// default to development. This guards the most common production misconfiguration
// (APP_ENV simply not set), which the validate()-only test cannot catch because it
// bypasses the env-reading default.
func TestLoad_AppEnvFailsClosed(t *testing.T) {
	unsetAppEnv := func(t *testing.T) {
		prev, had := os.LookupEnv("APP_ENV")
		os.Unsetenv("APP_ENV")
		t.Cleanup(func() {
			if had {
				os.Setenv("APP_ENV", prev)
			} else {
				os.Unsetenv("APP_ENV")
			}
		})
	}

	tests := []struct {
		name    string
		set     bool // whether to set APP_ENV at all
		value   string
		wantErr bool
	}{
		{"unset rejected", false, "", true},
		{"empty rejected", true, "", true},
		{"typo rejected", true, "prod", true},
		{"development accepted", true, "development", false},
		{"production accepted", true, "production", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Setenv("JWT_PRIVATE_KEY", "key")
			t.Setenv("SQLITE_PATH", "stone.db") // SQLite mode → no DB_URL/REDIS_URL needed
			if tt.value == "production" {
				t.Setenv("STEAM_API_KEY", "k")
				t.Setenv("STEAM_APP_ID", "480")
			}
			if tt.set {
				t.Setenv("APP_ENV", tt.value)
			} else {
				unsetAppEnv(t)
			}
			_, err := Load()
			if tt.wantErr && err == nil {
				t.Errorf("APP_ENV set=%v value=%q: want error, got nil", tt.set, tt.value)
			}
			if !tt.wantErr && err != nil {
				t.Errorf("APP_ENV set=%v value=%q: want nil, got %v", tt.set, tt.value, err)
			}
		})
	}
}

// R2: APP_ENV must be an exact whitelist member; a typo or empty value must fail
// the boot rather than silently falling through to the development branch.
func TestValidate_AppEnvWhitelist(t *testing.T) {
	base := func(env string) *Config {
		return &Config{
			AppEnv:     env,
			JWTPrivKey: "key",
			SQLitePath: "stone.db", // SQLite mode → DB_URL/REDIS_URL not required
		}
	}

	tests := []struct {
		name    string
		env     string
		wantErr bool
	}{
		{"development ok", "development", false},
		{"empty rejected", "", true},
		{"typo prod rejected", "prod", true},
		{"case-sensitive Production rejected", "Production", true},
		{"staging rejected", "staging", true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := base(tt.env).validate()
			if tt.wantErr && err == nil {
				t.Errorf("APP_ENV=%q: want error, got nil", tt.env)
			}
			if !tt.wantErr && err != nil {
				t.Errorf("APP_ENV=%q: want nil, got %v", tt.env, err)
			}
		})
	}
}

// production requires the Steam API key + app id (existing fail-closed behavior),
// and a valid APP_ENV alone is not sufficient.
func TestValidate_ProductionRequiresSteam(t *testing.T) {
	c := &Config{AppEnv: "production", JWTPrivKey: "key", SQLitePath: "stone.db"}
	if err := c.validate(); err == nil {
		t.Fatal("production without STEAM_API_KEY/STEAM_APP_ID: want error, got nil")
	}
	c.SteamAPIKey = "k"
	c.SteamAppID = "480"
	if err := c.validate(); err != nil {
		t.Errorf("production with steam vars: want nil, got %v", err)
	}
}
