package routes

import (
	"context"
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/coreos/go-oidc/v3/oidc"
	"github.com/pocketbase/pocketbase/core"
	"github.com/pocketbase/pocketbase/tests"
)

func TestMCPConfigFailsClosed(t *testing.T) {
	t.Setenv("SURMAI_MCP_ENABLED", "false")
	if config, err := MCPConfigFromEnv(); config != nil || err != nil {
		t.Fatal("disabled endpoint must not need config")
	}
	t.Setenv("SURMAI_MCP_ENABLED", "true")
	t.Setenv("SURMAI_MCP_PUBLIC_URL", "https://trip.example.test/mcp")
	t.Setenv("SURMAI_MCP_AUTH0_ISSUER", "https://issuer.example.test/")
	t.Setenv("SURMAI_MCP_IDENTITY_MAP", `{"auth0|test":"user_record_id"}`)
	if _, err := MCPConfigFromEnv(); err != nil {
		t.Fatal(err)
	}
	for _, bad := range []string{"http://trip.example.test/mcp", "https://user:password@trip.example.test/mcp", "https://trip.example.test/other", "https://trip.example.test/mcp?x=1"} {
		t.Setenv("SURMAI_MCP_PUBLIC_URL", bad)
		if _, err := MCPConfigFromEnv(); err == nil {
			t.Errorf("accepted invalid public URL %q", bad)
		}
	}
}

func TestMCPAuthenticationBoundaries(t *testing.T) {
	app, err := tests.NewTestApp(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer app.Cleanup()
	users, err := app.FindCollectionByNameOrId("users")
	if err != nil {
		t.Fatal(err)
	}
	user := core.NewRecord(users)
	user.SetEmail("mcp-auth@example.test")
	user.SetPassword("local-test-password-123")
	if err := app.Save(user); err != nil {
		t.Fatal(err)
	}
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	otherKey, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	config := &MCPConfig{PublicURL: "https://trip.example.test/mcp", Issuer: "https://issuer.example.test/", IdentityMap: map[string]string{"auth0|linked": user.Id}}
	verifier := oidc.NewVerifier(config.Issuer, &oidc.StaticKeySet{PublicKeys: []crypto.PublicKey{&key.PublicKey}}, &oidc.Config{ClientID: config.PublicURL, SupportedSigningAlgs: []string{"RS256"}})
	handler := newMCPHandlerWithVerifier(app, config, verifier)
	baseClaims := func() map[string]any {
		return map[string]any{"iss": config.Issuer, "aud": config.PublicURL, "sub": "auth0|linked", "scope": "surmai:read", "exp": time.Now().Add(time.Hour).Unix(), "iat": time.Now().Unix()}
	}
	sign := func(claims map[string]any, signingKey *rsa.PrivateKey) string {
		t.Helper()
		header := base64.RawURLEncoding.EncodeToString([]byte(`{"alg":"RS256","typ":"JWT"}`))
		payload, _ := json.Marshal(claims)
		unsigned := header + "." + base64.RawURLEncoding.EncodeToString(payload)
		sum := sha256.Sum256([]byte(unsigned))
		signature, err := rsa.SignPKCS1v15(rand.Reader, signingKey, crypto.SHA256, sum[:])
		if err != nil {
			t.Fatal(err)
		}
		return unsigned + "." + base64.RawURLEncoding.EncodeToString(signature)
	}
	cases := []struct {
		name   string
		mutate func(map[string]any)
		key    *rsa.PrivateKey
		status int
	}{
		{"valid", func(map[string]any) {}, key, 200},
		{"wrong audience", func(c map[string]any) { c["aud"] = "https://other.example.test" }, key, 401},
		{"wrong issuer", func(c map[string]any) { c["iss"] = "https://other.example.test/" }, key, 401},
		{"expired", func(c map[string]any) { c["exp"] = time.Now().Add(-time.Hour).Unix() }, key, 401},
		{"future nbf", func(c map[string]any) { c["nbf"] = time.Now().Add(time.Hour).Unix() }, key, 401},
		{"invalid signature", func(map[string]any) {}, otherKey, 401},
		{"unmapped identity", func(c map[string]any) { c["sub"] = "auth0|outsider" }, key, 403},
		{"missing scope", func(c map[string]any) { c["scope"] = "openid" }, key, 403},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			claims := baseClaims()
			tc.mutate(claims)
			r := httptest.NewRequest(http.MethodPost, "https://trip.example.test/mcp", strings.NewReader(`{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"get_profile","arguments":{}}}`)).WithContext(context.Background())
			r.Header.Set("Authorization", "Bearer "+sign(claims, tc.key))
			r.Header.Set("Content-Type", "application/json")
			r.Header.Set("Accept", "application/json, text/event-stream")
			r.Header.Set("MCP-Protocol-Version", "2025-11-25")
			w := httptest.NewRecorder()
			handler.ServeHTTP(w, r)
			if w.Code != tc.status {
				t.Fatalf("got %d want %d: %s", w.Code, tc.status, w.Body.String())
			}
			if tc.status == 200 && !strings.Contains(w.Body.String(), user.Id) {
				t.Fatalf("profile did not identify linked normal user: %s", w.Body.String())
			}
		})
	}
	for _, origin := range []string{"", "https://evil.example.test"} {
		r := httptest.NewRequest("POST", "https://trip.example.test/mcp", nil)
		r.Header.Set("Origin", origin)
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, r)
		expected := 401
		if origin != "" {
			expected = 403
		}
		if w.Code != expected {
			t.Errorf("unauthenticated origin %q got %d", origin, w.Code)
		}
		if origin == "" && !strings.Contains(w.Header().Get("WWW-Authenticate"), "oauth-protected-resource/mcp") {
			t.Error("missing resource discovery challenge")
		}
	}
}
