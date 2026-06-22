package auth

import (
	"context"
)

// mockSteamClient is used when APP_ENV == "development".
// Any ticket value except "invalid_ticket" is accepted and returns a fixed steam_id.
type mockSteamClient struct{}

func newMockSteamClient() SteamClient {
	return &mockSteamClient{}
}

func (m *mockSteamClient) AuthenticateTicket(_ context.Context, ticket string) (string, error) {
	if ticket == "invalid_ticket" {
		return "", ErrInvalidTicket
	}
	// Return the ticket value as the steam_id so load tests can create distinct
	// players by passing unique ticket strings (e.g. "7656119800000001").
	return ticket, nil
}
