package jobs

import (
	"time"

	"github.com/pocketbase/dbx"
	"github.com/pocketbase/pocketbase"
	"github.com/pocketbase/pocketbase/core"
)

type CleanupExpiredContentJob struct {
	Pb *pocketbase.PocketBase
}

func (job *CleanupExpiredContentJob) Execute() {
	app := job.Pb.App
	logger := app.Logger().WithGroup("CleanupExpiredContentJob")
	now := time.Now()

	// Delete all expired announcements
	expiredAnnouncements, err := app.FindAllRecords("announcements",
		dbx.NewExp("expiry < {:now}", dbx.Params{"now": now}))

	if err != nil {
		logger.Error("FindAllRecords error", "error", err)
	} else {
		for _, announcement := range expiredAnnouncements {
			if err := app.Delete(announcement); err != nil {
				logger.Error("Delete error", "announcementId", announcement.Id, "error", err)
			}
		}
		logger.Info("Deleted expired announcements", "count", len(expiredAnnouncements))
	}

	// Delete all expired notifications
	expiredNotifications, err := app.FindAllRecords("notifications",
		dbx.NewExp("expiry < {:now}", dbx.Params{"now": now}))

	if err != nil {
		logger.Error("FindAllRecords error", "error", err)
	} else {
		for _, notification := range expiredNotifications {
			if err := app.Delete(notification); err != nil {
				logger.Error("Delete error", "notificationId", notification.Id, "error", err)
			}
		}
		logger.Info("Deleted expired notifications", "count", len(expiredNotifications))
	}

	if err := expireTripAssistantProposals(app); err != nil {
		logger.Error("Expire trip assistant proposals error", "error", err)
	}
}

func expireTripAssistantProposals(app core.App) error {
	records, err := app.FindAllRecords(
		"trip_assistant_proposals",
		dbx.NewExp("status = 'pending' and expiresAt < {:now}", dbx.Params{"now": time.Now().UTC().Format(time.RFC3339)}),
	)
	if err != nil {
		return err
	}
	for _, record := range records {
		record.Set("status", "expired")
		if err := app.Save(record); err != nil {
			return err
		}
	}
	return nil
}
