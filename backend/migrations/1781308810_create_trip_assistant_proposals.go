package migrations

import (
	"github.com/pocketbase/pocketbase/core"
	m "github.com/pocketbase/pocketbase/migrations"
	"github.com/pocketbase/pocketbase/tools/types"
)

func init() {
	m.Register(func(app core.App) error {
		existing, _ := app.FindCollectionByNameOrId("trip_assistant_proposals")
		if existing != nil {
			return nil
		}

		trips, err := app.FindCollectionByNameOrId("trips")
		if err != nil {
			return err
		}

		users, err := app.FindCollectionByNameOrId("users")
		if err != nil {
			return err
		}

		proposals := core.NewBaseCollection("trip_assistant_proposals")
		proposals.Fields.Add(
			&core.RelationField{
				Name:          "trip",
				CollectionId:  trips.Id,
				CascadeDelete: true,
				Required:      true,
				MaxSelect:     1,
			},
			&core.RelationField{
				Name:          "user",
				CollectionId:  users.Id,
				CascadeDelete: true,
				Required:      true,
				MaxSelect:     1,
			},
			&core.SelectField{
				Name:     "status",
				Required: true,
				Values:   []string{"pending", "approved", "rejected", "expired", "applying", "failed"},
			},
			&core.TextField{Name: "actionType", Required: true},
			&core.JSONField{Name: "changes", MaxSize: 20000},
			&core.TextField{Name: "summary"},
			&core.JSONField{Name: "preview", MaxSize: 20000},
			&core.JSONField{Name: "sources", MaxSize: 10000},
			&core.TextField{Name: "sdkState", Max: 1000000},
			&core.JSONField{Name: "sdkInterruptions", MaxSize: 50000},
			&core.JSONField{Name: "result", MaxSize: 20000},
			&core.TextField{Name: "error"},
			&core.DateField{Name: "expiresAt", Required: true},
			&core.AutodateField{Name: "created", OnCreate: true, OnUpdate: false},
			&core.AutodateField{Name: "updated", OnCreate: true, OnUpdate: true},
		)

		accessRule := "user = @request.auth.id && (trip.ownerId = @request.auth.id || trip.collaborators.id ?= @request.auth.id)"
		proposals.ListRule = types.Pointer(accessRule)
		proposals.ViewRule = types.Pointer(accessRule)
		proposals.CreateRule = types.Pointer(accessRule)
		proposals.UpdateRule = types.Pointer(accessRule)
		proposals.DeleteRule = types.Pointer(accessRule)
		proposals.AddIndex("idx_trip_assistant_proposals_trip_user_status", false, "trip,user,status", "")
		proposals.AddIndex("idx_trip_assistant_proposals_expires", false, "expiresAt", "")

		return app.Save(proposals)
	}, func(app core.App) error {
		proposals, err := app.FindCollectionByNameOrId("trip_assistant_proposals")
		if err != nil {
			return err
		}
		return app.Delete(proposals)
	})
}
