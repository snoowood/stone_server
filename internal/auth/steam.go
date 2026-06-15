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

// ErrAccountBanned / ErrTicketNotOwned are specific rejection reasons that wrap
// ErrInvalidTicket so callers can keep matching errors.Is(err, ErrInvalidTicket)
// (single 401 INVALID_TICKET response) while logs retain the precise reason.
var (
	// ErrAccountBanned: the Steam account is VAC- or publisher-banned for this app.
	ErrAccountBanned = fmt.Errorf("steam account banned: %w", ErrInvalidTicket)
	// ErrTicketNotOwned: the ticket owner differs from the authenticating account,
	// i.e. Family Sharing — disallowed per product policy (treat as not owning the game).
	ErrTicketNotOwned = fmt.Errorf("steam ticket owner mismatch (family sharing): %w", ErrInvalidTicket)
)

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
// apiKey should be a Steam *publisher* Web API key (Steamworks → Users & Permissions →
// Manage Groups → Generate Web API Key). AuthenticateUserTicket itself accepts a standard
// or publisher key, but the same configured key also drives achievement sync
// (ISteamUserStats/SetUserStatsForGame, publisher-only), so a publisher key is required
// server-wide — NOT the individual user key from steamcommunity.com/dev/apikey.
func NewSteamClient(apiKey, appID string) SteamClient {
	return &steamClient{
		apiKey: apiKey,
		appID:  appID,
		http:   &http.Client{Timeout: 5 * time.Second},
	}
}

// ticketParams is the relevant subset of AuthenticateUserTicket response params.
type ticketParams struct {
	Result          string `json:"result"`
	SteamID         string `json:"steamid"`
	OwnerSteamID    string `json:"ownersteamid"`
	VacBanned       bool   `json:"vacbanned"`
	PublisherBanned bool   `json:"publisherbanned"`
}

type steamAuthResp struct {
	Response struct {
		Params *ticketParams `json:"params"`
		Error  *struct {
			ErrorCode int    `json:"errorcode"`
			ErrDesc   string `json:"errordesc"`
		} `json:"error"`
	} `json:"response"`
}

// validateTicketParams enforces the server-authoritative login gate (pure function,
// unit-tested independently of the HTTP call):
//   - result must be "OK" (Steam already validates the ticket matches the queried appid)
//   - VAC / publisher bans are rejected
//   - Family Sharing is disallowed (strict, fail-closed): ownersteamid must be present
//     AND equal to the authenticating SteamID. A missing ownersteamid on an OK result is
//     anomalous and rejected too — leniency there would silently bypass the policy if
//     Steam ever omits the field for a borrowed license.
func validateTicketParams(p *ticketParams) (string, error) {
	if p == nil || p.Result != "OK" {
		return "", ErrInvalidTicket
	}
	if p.VacBanned || p.PublisherBanned {
		return "", ErrAccountBanned
	}
	if p.OwnerSteamID == "" || p.OwnerSteamID != p.SteamID {
		return "", ErrTicketNotOwned
	}
	return p.SteamID, nil
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

	return validateTicketParams(sr.Response.Params)
}
