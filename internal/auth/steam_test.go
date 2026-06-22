package auth

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"strings"
	"testing"
)

// capturingRoundTripper records the outgoing request and returns a canned response
// without hitting the network.
type capturingRoundTripper struct {
	req      *http.Request
	bodyText string
	respBody string
}

func (c *capturingRoundTripper) RoundTrip(r *http.Request) (*http.Response, error) {
	c.req = r
	if r.Body != nil {
		b, _ := io.ReadAll(r.Body)
		c.bodyText = string(b)
	}
	return &http.Response{
		StatusCode: http.StatusOK,
		Body:       io.NopCloser(strings.NewReader(c.respBody)),
		Header:     make(http.Header),
	}, nil
}

// P0-4 regression: the publisher key + ticket must travel in the POST form body,
// never in the URL (where they would leak into url.Error logs / proxy access logs /
// metric path labels).
func TestAuthenticateTicket_UsesPostFormBody(t *testing.T) {
	const (
		secretKey    = "PUBLISHER-SECRET-KEY"
		secretTicket = "TICKET-ABCDEF"
	)
	rt := &capturingRoundTripper{
		respBody: `{"response":{"params":{"result":"OK","steamid":"7656119","ownersteamid":"7656119"}}}`,
	}
	c := &steamClient{apiKey: secretKey, appID: "480", http: &http.Client{Transport: rt}}

	id, err := c.AuthenticateTicket(context.Background(), secretTicket)
	if err != nil {
		t.Fatalf("AuthenticateTicket: %v", err)
	}
	if id != "7656119" {
		t.Errorf("steamID: want 7656119, got %q", id)
	}
	if rt.req.Method != http.MethodPost {
		t.Errorf("method: want POST, got %s", rt.req.Method)
	}
	// No secrets in the URL at all.
	if rt.req.URL.RawQuery != "" {
		t.Errorf("URL must carry no query, got %q", rt.req.URL.RawQuery)
	}
	full := rt.req.URL.String()
	if strings.Contains(full, secretKey) || strings.Contains(full, secretTicket) {
		t.Errorf("URL leaked a secret: %s", full)
	}
	if ct := rt.req.Header.Get("Content-Type"); ct != "application/x-www-form-urlencoded" {
		t.Errorf("Content-Type: want form-urlencoded, got %q", ct)
	}
	form, err := url.ParseQuery(rt.bodyText)
	if err != nil {
		t.Fatalf("parse body: %v", err)
	}
	if form.Get("key") != secretKey || form.Get("ticket") != secretTicket ||
		form.Get("appid") != "480" || form.Get("identity") != "stone-server" {
		t.Errorf("form body missing expected params: %v", form)
	}
}

func TestValidateTicketParams(t *testing.T) {
	tests := []struct {
		name    string
		params  *ticketParams
		wantID  string
		wantErr error
	}{
		{"nil params", nil, "", ErrInvalidTicket},
		{"result not OK", &ticketParams{Result: "Expired", SteamID: "7656119"}, "", ErrInvalidTicket},
		{"ok owned", &ticketParams{Result: "OK", SteamID: "7656119", OwnerSteamID: "7656119"}, "7656119", nil},
		{"empty owner rejected (strict)", &ticketParams{Result: "OK", SteamID: "7656119"}, "", ErrTicketNotOwned},
		{"vac banned", &ticketParams{Result: "OK", SteamID: "7656119", OwnerSteamID: "7656119", VacBanned: true}, "", ErrAccountBanned},
		{"publisher banned", &ticketParams{Result: "OK", SteamID: "7656119", OwnerSteamID: "7656119", PublisherBanned: true}, "", ErrAccountBanned},
		{"family sharing owner mismatch", &ticketParams{Result: "OK", SteamID: "7656119", OwnerSteamID: "9999999"}, "", ErrTicketNotOwned},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			id, err := validateTicketParams(tt.params)
			if id != tt.wantID {
				t.Errorf("steamID: want %q, got %q", tt.wantID, id)
			}
			if tt.wantErr == nil {
				if err != nil {
					t.Errorf("want nil err, got %v", err)
				}
				return
			}
			if !errors.Is(err, tt.wantErr) {
				t.Errorf("want errors.Is %v, got %v", tt.wantErr, err)
			}
			// 모든 거절 사유는 ErrInvalidTicket 로 매핑되어 핸들러가 401 INVALID_TICKET 로 응답한다.
			if !errors.Is(err, ErrInvalidTicket) {
				t.Errorf("rejection must wrap ErrInvalidTicket, got %v", err)
			}
		})
	}
}

// 응답 JSON 의 신규 필드(ownersteamid/vacbanned/publisherbanned)가 정확히 디코드되는지.
func TestSteamAuthResp_Decode(t *testing.T) {
	body := `{"response":{"params":{"result":"OK","steamid":"76561197960435530","ownersteamid":"76561197960435530","vacbanned":false,"publisherbanned":false}}}`
	var sr steamAuthResp
	if err := json.Unmarshal([]byte(body), &sr); err != nil {
		t.Fatalf("decode: %v", err)
	}
	p := sr.Response.Params
	if p == nil {
		t.Fatal("params nil")
	}
	if p.Result != "OK" || p.SteamID != "76561197960435530" || p.OwnerSteamID != "76561197960435530" {
		t.Errorf("unexpected params: %+v", p)
	}
	if p.VacBanned || p.PublisherBanned {
		t.Errorf("expected no bans: %+v", p)
	}
	id, err := validateTicketParams(p)
	if err != nil || id != "76561197960435530" {
		t.Errorf("validate OK ticket: id=%q err=%v", id, err)
	}
}
