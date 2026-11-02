package routes

import (
	"testing"

	"github.com/pocketbase/pocketbase/core"
	"github.com/pocketbase/pocketbase/tests"
)

func TestMCPCanCreate(t *testing.T) {
	app, err := tests.NewTestApp(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer app.Cleanup()
	users, err := app.FindCollectionByNameOrId("users")
	if err != nil {
		t.Fatal(err)
	}
	trips := core.NewBaseCollection("trips")
	trips.Fields.Add(&core.RelationField{Name: "owner", CollectionId: users.Id, MaxSelect: 1})
	trips.Fields.Add(&core.RelationField{Name: "collaborators", CollectionId: users.Id, MaxSelect: 10})
	if err := app.Save(trips); err != nil {
		t.Fatal(err)
	}
	activities := core.NewBaseCollection("activities")
	activities.Fields.Add(&core.RelationField{Name: "trip", CollectionId: trips.Id, MaxSelect: 1})
	activities.Fields.Add(&core.TextField{Name: "name"})
	rule := "(trip.owner = @request.auth.id || trip.collaborators.id ?= @request.auth.id) && @request.body.name = 'Allowed'"
	activities.CreateRule = &rule
	if err := app.Save(activities); err != nil {
		t.Fatal(err)
	}
	makeUser := func(email string) *core.Record {
		r := core.NewRecord(users)
		r.SetEmail(email)
		r.SetPassword("test-password-123")
		if err := app.Save(r); err != nil {
			t.Fatal(err)
		}
		return r
	}
	owner, collaborator, other := makeUser("owner@example.test"), makeUser("collaborator@example.test"), makeUser("other@example.test")
	trip := core.NewRecord(trips)
	trip.Set("owner", owner.Id)
	trip.Set("collaborators", []string{collaborator.Id})
	if err := app.Save(trip); err != nil {
		t.Fatal(err)
	}
	activity := core.NewRecord(activities)
	activity.Set("trip", trip.Id)
	activity.Set("name", "Allowed")
	if !mcpCanCreate(app, owner, activity) {
		t.Error("owner should be allowed")
	}
	if !mcpCanCreate(app, collaborator, activity) {
		t.Error("collaborator should be allowed")
	}
	if mcpCanCreate(app, other, activity) {
		t.Error("unrelated user should be denied")
	}
	activity.Set("name", "Denied")
	if mcpCanCreate(app, owner, activity) {
		t.Error("request body rule should be enforced")
	}
	if count, err := app.CountRecords("activities"); err != nil || count != 0 {
		t.Errorf("checker must not save records: count=%d err=%v", count, err)
	}
}
