package model

import (
	"bytes"
	"encoding/json"
	"reflect"
	"testing"
)

func TestUserJSONOmitsPasswordHash(t *testing.T) {
	const passwordHash = "$2a$10$example-sensitive-password-hash"
	raw, err := json.Marshal(User{
		ID:   42,
		User: "ordinary-user",
		Pwd:  passwordHash,
	})
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(raw, []byte(passwordHash)) {
		t.Fatalf("password hash leaked into JSON: %s", raw)
	}

	var fields map[string]any
	if err := json.Unmarshal(raw, &fields); err != nil {
		t.Fatal(err)
	}
	if _, exists := fields["pwd"]; exists {
		t.Fatalf("password field leaked into JSON: %s", raw)
	}
}

func TestUserTokenVersionContract(t *testing.T) {
	field, ok := reflect.TypeOf(User{}).FieldByName("TokenVersion")
	if !ok {
		t.Fatal("User.TokenVersion field is missing")
	}
	if field.Type.Kind() != reflect.Int64 {
		t.Fatalf("TokenVersion type=%s, want int64", field.Type)
	}
	if got := field.Tag.Get("gorm"); got != "column:token_version;not null;default:0" {
		t.Fatalf("TokenVersion gorm tag=%q", got)
	}
	if got := field.Tag.Get("json"); got != "-" {
		t.Fatalf("TokenVersion json tag=%q, want hidden", got)
	}
}

func TestNodeJSONOmitsHistoricalPanelBaseURL(t *testing.T) {
	raw, err := json.Marshal(Node{
		ID:                    7,
		Name:                  "private-route-node",
		LastConnectedBaseURL:  "https://internal-panel.example.com/base",
		LastConnectedBaseTime: 123456,
	})
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(raw, []byte("internal-panel.example.com")) || bytes.Contains(raw, []byte("lastConnectedBase")) {
		t.Fatalf("historical panel URL leaked into JSON: %s", raw)
	}
}
