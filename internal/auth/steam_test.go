package auth

import (
	"encoding/json"
	"errors"
	"testing"
)

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
