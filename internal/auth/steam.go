package auth

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"time"
)

// ErrInvalidTicket is returned when Steam rejects the ticket.
var ErrInvalidTicket = errors.New("invalid steam ticket")

// SteamClient validates Steam auth tickets.
type SteamClient interface {
	AuthenticateTicket(ctx context.Context, ticket string) (steamID string, err error)
}

type steamClient struct {
	apiKey string
	appID  string
	http   *http.Client
}

// NewSteamClientForEnv returns a mock SteamClient in non-production environments
// and a real Steamworks-backed client in production.
func NewSteamClientForEnv(appEnv, apiKey, appID string) SteamClient {
	if appEnv != "production" {
		return newMockSteamClient()
	}
	return NewSteamClient(apiKey, appID)
}

// NewSteamClient returns a SteamClient backed by the Steamworks Web API.
func NewSteamClient(apiKey, appID string) SteamClient {
	return &steamClient{
		apiKey: apiKey,
		appID:  appID,
		http:   &http.Client{Timeout: 5 * time.Second},
	}
}

type steamAuthResp struct {
	Response struct {
		Params *struct {
			Result  string `json:"result"`
			SteamID string `json:"steamid"`
		} `json:"params"`
		Error *struct {
			ErrorCode int    `json:"errorcode"`
			ErrDesc   string `json:"errordesc"`
		} `json:"error"`
	} `json:"response"`
}

func (c *steamClient) AuthenticateTicket(ctx context.Context, ticket string) (string, error) {
	params := url.Values{}
	params.Set("key", c.apiKey)
	params.Set("appid", c.appID)
	params.Set("ticket", ticket)
	params.Set("identity", "stone-server")

	endpoint := "https://partner.steam-api.com/ISteamUserAuth/AuthenticateUserTicket/v1/?" + params.Encode()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return "", fmt.Errorf("build steam request: %w", err)
	}

	resp, err := c.http.Do(req)
	if err != nil {
		return "", fmt.Errorf("steam api call: %w", err)
	}
	defer resp.Body.Close()

	var sr steamAuthResp
	if err := json.NewDecoder(resp.Body).Decode(&sr); err != nil {
		return "", fmt.Errorf("decode steam response: %w", err)
	}

	if sr.Response.Params == nil || sr.Response.Params.Result != "OK" {
		return "", ErrInvalidTicket
	}
	return sr.Response.Params.SteamID, nil
}
