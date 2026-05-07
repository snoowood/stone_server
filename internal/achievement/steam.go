package achievement

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// SteamAchievementClient syncs an achievement unlock to Steam.
type SteamAchievementClient interface {
	SetAchievement(ctx context.Context, steamID, achievementID string) error
}

// NewSteamAchievementClientForEnv returns a mock in non-production environments.
func NewSteamAchievementClientForEnv(appEnv, apiKey, appID string) SteamAchievementClient {
	if appEnv != "production" {
		return &mockSteamAchievementClient{}
	}
	return &steamAchievementClient{
		apiKey: apiKey,
		appID:  appID,
		http:   &http.Client{Timeout: 5 * time.Second},
	}
}

type mockSteamAchievementClient struct{}

func (m *mockSteamAchievementClient) SetAchievement(_ context.Context, _, _ string) error {
	return nil
}

type steamAchievementClient struct {
	apiKey string
	appID  string
	http   *http.Client
}

// SetAchievement calls ISteamUserStats/SetUserStatsForGame/v1/ to unlock a Steam achievement.
func (c *steamAchievementClient) SetAchievement(ctx context.Context, steamID, achievementID string) error {
	params := url.Values{}
	params.Set("key", c.apiKey)
	params.Set("steamid", steamID)
	params.Set("appid", c.appID)
	params.Set("count", "1")
	params.Set("name[0]", achievementID)
	params.Set("value[0]", "1")

	req, err := http.NewRequestWithContext(ctx, http.MethodPost,
		"https://partner.steam-api.com/ISteamUserStats/SetUserStatsForGame/v1/",
		strings.NewReader(params.Encode()),
	)
	if err != nil {
		return fmt.Errorf("build steam achievement request: %w", err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	resp, err := c.http.Do(req)
	if err != nil {
		return fmt.Errorf("steam achievement api: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("steam achievement api: status %d", resp.StatusCode)
	}
	return nil
}
