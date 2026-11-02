package app

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/pocketbase/pocketbase"
	"github.com/pocketbase/pocketbase/apis"
	"github.com/pocketbase/pocketbase/core"
)

func TestMCPApplicationRouterStartup(t *testing.T) {
	t.Setenv("SURMAI_MCP_ENABLED", "true")
	t.Setenv("SURMAI_MCP_PUBLIC_URL", "https://trip.example.test/mcp")
	t.Setenv("SURMAI_MCP_AUTH0_ISSUER", "https://issuer.example.test/")
	t.Setenv("SURMAI_MCP_IDENTITY_MAP", `{"auth0|test":"test_user_id"}`)
	pb := pocketbase.NewWithConfig(pocketbase.Config{DefaultDataDir: t.TempDir()})
	if err := pb.Bootstrap(); err != nil {
		t.Fatal(err)
	}
	defer pb.ResetBootstrapState()
	if err := pb.RunAllMigrations(); err != nil {
		t.Fatal(err)
	}
	surmai := &SurmaiApp{Pb: pb}
	surmai.BindRoutes()
	router, err := apis.NewRouter(pb)
	if err != nil {
		t.Fatal(err)
	}
	event := &core.ServeEvent{App: pb, Router: router, Server: &http.Server{Addr: "127.0.0.1:0"}}
	if err := pb.OnServe().Trigger(event); err != nil {
		t.Fatal(err)
	}
	mux, err := router.BuildMux()
	if err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct {
		method, path string
		status       int
	}{
		{"GET", "/api/health", 200},
		{"GET", "/.well-known/oauth-protected-resource/mcp", 200},
		{"POST", "/mcp", 401},
		{"GET", "/mcp", 401},
		{"DELETE", "/mcp", 401},
	} {
		request := httptest.NewRequest(test.method, "https://trip.example.test"+test.path, nil)
		response := httptest.NewRecorder()
		mux.ServeHTTP(response, request)
		if response.Code != test.status {
			t.Errorf("%s %s got %d, want %d", test.method, test.path, response.Code, test.status)
		}
	}
}
