package routes

import (
	"archive/zip"
	"backend/trips"
	"context"
	"encoding/json"
	"github.com/gosticks/openai-responses-api-go/client"
	openairesponses "github.com/gosticks/openai-responses-api-go"
	"github.com/pocketbase/pocketbase/core"
	"net/http"
	"os"
)

func GetAIIterary(e *core.RequestEvent) error {
	trip := e.Get("trip").(*core.Record)

	tripExport, err := os.CreateTemp("", "trip-export-")
	if err != nil {
		return err
	}
	defer tripExport.Close()

	err = trips.ExportTripArchive(e.App, trip, tripExport)
	if err != nil {
		return err
	}

	zipFile, err := os.Open(tripExport.Name())
	if err != nil {
		return err
	}
	defer zipFile.Close()

	stat, err := zipFile.Stat()
	if err != nil {
		return err
	}

	reader, err := zip.NewReader(zipFile, stat.Size())
	if err != nil {
		return err
	}

	var tripData map[string]interface{}
	for _, file := range reader.File {
		if file.Name == "trip.json" {
			f, err := file.Open()
			if err != nil {
				return err
			}
			defer f.Close()

			if err := json.NewDecoder(f).Decode(&tripData); err != nil {
				return err
			}
			break
		}
	}

	if tripData == nil {
		return e.JSON(http.StatusNotFound, map[string]string{
			"message": "trip.json not found in archive",
		})
	}

	tripDataBytes, err := json.Marshal(tripData)
	if err != nil {
		return err
	}

	c := client.NewClient(os.Getenv("OPENAI_API_KEY"))
	resp, err := c.Responses.Create(
		context.Background(),
		openairesponses.ResponseRequest{
			Model: "gpt-5-mini",
			Messages: []openairesponses.ResponseInputMessage{
				{
					Role:    "user",
					Content: string(tripDataBytes),
				},
			},
		},
	)

	if err != nil {
		return err
	}

	return e.JSON(http.StatusOK, resp)
}
