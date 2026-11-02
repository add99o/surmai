package routes

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/pocketbase/pocketbase/core"
	"github.com/pocketbase/pocketbase/tests"
)

func TestMCPIntegration(t *testing.T) {
	app, err := tests.NewTestApp(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer app.Cleanup()
	users, err := app.FindCollectionByNameOrId("users")
	if err != nil {
		t.Fatal(err)
	}
	makeUser := func(email string) *core.Record {
		u := core.NewRecord(users)
		u.SetEmail(email)
		u.SetPassword("test-password-123")
		if err := app.Save(u); err != nil {
			t.Fatal(err)
		}
		return u
	}
	owner, outsider := makeUser("owner@example.test"), makeUser("outsider@example.test")
	trips := core.NewBaseCollection("trips")
	trips.Fields.Add(&core.TextField{Name: "name"}, &core.DateField{Name: "startDate"}, &core.DateField{Name: "endDate"}, &core.RelationField{Name: "ownerId", CollectionId: users.Id, MaxSelect: 1}, &core.RelationField{Name: "collaborators", CollectionId: users.Id, MaxSelect: 20}, &core.AutodateField{Name: "updated", OnCreate: true, OnUpdate: true})
	tripRule := "ownerId = @request.auth.id || collaborators.id ?= @request.auth.id"
	trips.ListRule, trips.ViewRule, trips.UpdateRule = &tripRule, &tripRule, &tripRule
	if err := app.Save(trips); err != nil {
		t.Fatal(err)
	}
	activities := core.NewBaseCollection("activities")
	activities.Fields.Add(&core.RelationField{Name: "trip", CollectionId: trips.Id, MaxSelect: 1}, &core.TextField{Name: "name", Required: true}, &core.DateField{Name: "startDate", Required: true}, &core.AutodateField{Name: "created", OnCreate: true}, &core.AutodateField{Name: "updated", OnCreate: true, OnUpdate: true})
	childRule := "trip.ownerId = @request.auth.id || trip.collaborators.id ?= @request.auth.id"
	activities.ListRule, activities.ViewRule, activities.CreateRule, activities.UpdateRule, activities.DeleteRule = &childRule, &childRule, &childRule, &childRule, &childRule
	activities.AddIndex("idx_mcp_activity_unique", true, "name", "")
	if err := app.Save(activities); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"lodgings", "transportations", "trip_expenses", "trip_attachments"} {
		collection := core.NewBaseCollection(name)
		collection.Fields.Add(&core.RelationField{Name: "trip", CollectionId: trips.Id, MaxSelect: 1}, &core.TextField{Name: "name"}, &core.AutodateField{Name: "created", OnCreate: true})
		collection.ListRule, collection.ViewRule = &childRule, &childRule
		if err := app.Save(collection); err != nil {
			t.Fatal(err)
		}
	}
	proposalsCollection := core.NewBaseCollection("trip_assistant_proposals")
	proposalsCollection.Fields.Add(&core.RelationField{Name: "trip", CollectionId: trips.Id, MaxSelect: 1}, &core.RelationField{Name: "user", CollectionId: users.Id, MaxSelect: 1}, &core.TextField{Name: "status"}, &core.TextField{Name: "actionType"}, &core.TextField{Name: "summary"}, &core.JSONField{Name: "changes"}, &core.JSONField{Name: "preview"}, &core.JSONField{Name: "sources"}, &core.JSONField{Name: "sdkInterruptions"}, &core.TextField{Name: "sdkState"}, &core.JSONField{Name: "result"}, &core.TextField{Name: "error"}, &core.DateField{Name: "expiresAt"})
	if err := app.Save(proposalsCollection); err != nil {
		t.Fatal(err)
	}
	trip := core.NewRecord(trips)
	trip.Set("name", "Test trip")
	trip.Set("ownerId", owner.Id)
	trip.Set("startDate", "2026-01-01 00:00:00.000Z")
	trip.Set("endDate", "2026-01-10 00:00:00.000Z")
	if err := app.Save(trip); err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	config := &MCPConfig{PublicURL: "https://example.test/mcp"}
	connect := func(user *core.Record, write bool) *mcp.ClientSession {
		identity := mcpIdentity{user: user, scopes: map[string]bool{"surmai:read": true, "surmai:write": write}}
		h := mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server { return newSurmaiMCPServer(app, config, identity) }, &mcp.StreamableHTTPOptions{Stateless: true, JSONResponse: true})
		srv := httptest.NewServer(h)
		t.Cleanup(srv.Close)
		session, err := mcp.NewClient(&mcp.Implementation{Name: "integration-test", Version: "1"}, nil).Connect(ctx, &mcp.StreamableClientTransport{Endpoint: srv.URL}, nil)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = session.Close() })
		return session
	}
	ownerClient, readClient, outsiderClient := connect(owner, true), connect(owner, false), connect(outsider, true)
	tools, err := ownerClient.ListTools(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	expected := map[string]bool{"get_profile": true, "list_trips": true, "get_trip_itinerary": true, "list_expenses": true, "list_attachments": true, "propose_itinerary_changes": true, "get_proposal": true}
	if len(tools.Tools) != len(expected) {
		t.Fatalf("unexpected tool count: %d", len(tools.Tools))
	}
	for _, tool := range tools.Tools {
		if !expected[tool.Name] {
			t.Fatalf("unexpected tool: %s", tool.Name)
		}
		delete(expected, tool.Name)
	}
	if len(expected) != 0 {
		t.Fatalf("missing tools: %v", expected)
	}
	call := func(s *mcp.ClientSession, name string, args any) *mcp.CallToolResult {
		result, err := s.CallTool(ctx, &mcp.CallToolParams{Name: name, Arguments: args})
		if err != nil {
			t.Fatal(err)
		}
		return result
	}
	content := func(r *mcp.CallToolResult) string {
		if r.IsError {
			t.Fatalf("tool error: %+v", r.Content[0].(*mcp.TextContent))
		}
		b, err := json.Marshal(r.StructuredContent)
		if err != nil {
			t.Fatal(err)
		}
		return string(b)
	}
	if got := content(call(ownerClient, "get_profile", map[string]any{})); !strings.Contains(got, owner.Id) {
		t.Fatalf("profile missing owner id: %s", got)
	}
	if got := content(call(ownerClient, "list_trips", map[string]any{})); !strings.Contains(got, "Test trip") {
		t.Fatalf("owner trip missing: %s", got)
	}
	if got := content(call(outsiderClient, "list_trips", map[string]any{})); strings.Contains(got, trip.Id) || !strings.Contains(got, `"trips":[]`) {
		t.Fatalf("outsider trip list: %s", got)
	}
	if got := content(call(ownerClient, "get_trip_itinerary", map[string]any{"trip_id": trip.Id})); !strings.Contains(got, "Test trip") {
		t.Fatalf("itinerary missing trip: %s", got)
	}
	for _, name := range []string{"list_expenses", "list_attachments"} {
		content(call(ownerClient, name, map[string]any{"trip_id": trip.Id}))
	}
	if r := call(outsiderClient, "get_trip_itinerary", map[string]any{"trip_id": trip.Id}); !r.IsError {
		t.Fatal("outsider read should fail")
	}
	change := map[string]any{"operation": "create", "entity_type": "activity", "fields": map[string]any{"name": "Museum", "start_time": "2026-01-02T10:00:00Z"}}
	proposalArgs := map[string]any{"trip_id": trip.Id, "title": "Museum", "summary": "Add visit", "changes": []any{change}}
	if r := call(readClient, "propose_itinerary_changes", proposalArgs); !r.IsError {
		t.Fatal("read-only user proposed a write")
	}
	if r := call(ownerClient, "propose_itinerary_changes", proposalArgs); r.IsError {
		t.Fatalf("proposal: %+v content=%+v", r, r.Content[0].(*mcp.TextContent))
	}
	if n, err := app.CountRecords("activities"); err != nil || n != 0 {
		t.Fatalf("proposal wrote activity: %d %v", n, err)
	}
	proposals, err := app.FindAllRecords("trip_assistant_proposals")
	if err != nil || len(proposals) != 1 {
		t.Fatalf("proposals: %d %v", len(proposals), err)
	}
	proposal := proposals[0]
	if _, err := approveMCPProposal(app, trip, owner.Id, proposal); err != nil {
		t.Fatalf("approve: %v", err)
	}
	if n, err := app.CountRecords("activities"); err != nil || n != 1 {
		t.Fatalf("approval activities: %d %v", n, err)
	}
	if _, err := approveMCPProposal(app, trip, owner.Id, proposal); err == nil {
		t.Fatal("second approval succeeded")
	}
	if r := call(ownerClient, "propose_itinerary_changes", proposalArgs); r.IsError {
		t.Fatalf("stale proposal: %+v", r.Content[0].(*mcp.TextContent))
	}
	proposals, err = app.FindAllRecords("trip_assistant_proposals")
	if err != nil {
		t.Fatal(err)
	}
	var pending *core.Record
	for _, p := range proposals {
		if p.GetString("status") == "pending" {
			pending = p
		}
	}
	if pending == nil {
		t.Fatal("missing pending proposal")
	}
	trip.Set("name", "Changed trip")
	if err := app.Save(trip); err != nil {
		t.Fatal(err)
	}
	if _, err := approveMCPProposal(app, trip, owner.Id, pending); err == nil {
		t.Fatal("stale approval succeeded")
	}
	duplicate := map[string]any{"operation": "create", "entity_type": "activity", "fields": map[string]any{"name": "Duplicate", "start_time": "2026-01-02T10:00:00Z"}}
	batch := map[string]any{"trip_id": trip.Id, "title": "Batch", "summary": "Two conflicting changes", "changes": []any{duplicate, duplicate}}
	if r := call(ownerClient, "propose_itinerary_changes", batch); r.IsError {
		t.Fatalf("batch proposal: %+v", r.Content[0].(*mcp.TextContent))
	}
	proposals, err = app.FindAllRecords("trip_assistant_proposals")
	if err != nil {
		t.Fatal(err)
	}
	for _, p := range proposals {
		if p.GetString("summary") == "Two conflicting changes" {
			pending = p
		}
	}
	if _, err := approveMCPProposal(app, trip, owner.Id, pending); err == nil {
		t.Fatal("duplicate batch succeeded")
	}
	if n, err := app.CountRecords("activities"); err != nil || n != 1 {
		t.Fatalf("batch was not atomic: %d %v", n, err)
	}
}
