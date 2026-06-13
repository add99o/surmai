package migrations

import (
	"github.com/pocketbase/pocketbase/core"
	m "github.com/pocketbase/pocketbase/migrations"
)

func init() {
	m.Register(func(app core.App) error {
		proposals, err := app.FindCollectionByNameOrId("trip_assistant_proposals")
		if err != nil {
			return err
		}

		sdkStateField := proposals.Fields.GetByName("sdkState").(*core.TextField)
		sdkStateField.Max = 1000000

		return app.Save(proposals)
	}, func(app core.App) error {
		proposals, err := app.FindCollectionByNameOrId("trip_assistant_proposals")
		if err != nil {
			return err
		}

		sdkStateField := proposals.Fields.GetByName("sdkState").(*core.TextField)
		sdkStateField.Max = 0

		return app.Save(proposals)
	})
}
