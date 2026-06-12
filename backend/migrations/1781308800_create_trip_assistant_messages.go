package migrations

import (
	"github.com/pocketbase/pocketbase/core"
	m "github.com/pocketbase/pocketbase/migrations"
	"github.com/pocketbase/pocketbase/tools/types"
)

func init() {
	m.Register(func(app core.App) error {
		existing, _ := app.FindCollectionByNameOrId("trip_assistant_messages")
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

		messages := core.NewBaseCollection("trip_assistant_messages")
		messages.Fields.Add(
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
				Name:     "role",
				Required: true,
				Values:   []string{"user", "assistant"},
			},
			&core.TextField{
				Name:     "content",
				Required: true,
			},
			&core.JSONField{
				Name:    "metadata",
				MaxSize: 10000,
			},
			&core.AutodateField{
				Name:     "created",
				OnCreate: true,
				OnUpdate: false,
			},
			&core.AutodateField{
				Name:     "updated",
				OnCreate: true,
				OnUpdate: true,
			},
		)

		accessRule := "user = @request.auth.id && (trip.ownerId = @request.auth.id || trip.collaborators.id ?= @request.auth.id)"
		messages.ListRule = types.Pointer(accessRule)
		messages.ViewRule = types.Pointer(accessRule)
		messages.CreateRule = types.Pointer(accessRule)
		messages.UpdateRule = types.Pointer(accessRule)
		messages.DeleteRule = types.Pointer(accessRule)

		return app.Save(messages)
	}, func(app core.App) error {
		messages, err := app.FindCollectionByNameOrId("trip_assistant_messages")
		if err != nil {
			return err
		}

		return app.Delete(messages)
	})
}
