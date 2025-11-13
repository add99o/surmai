package routes

import (
	"archive/zip"
	"backend/trips"
	"context"
	"encoding/json"
	"net/http"
	"os"

	"github.com/pocketbase/pocketbase/core"

	"github.com/openai/openai-go"
	"github.com/openai/openai-go/option"
	"github.com/openai/openai-go/responses"
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

	apiKey := os.Getenv("OPENAI_API_KEY")
	oaiClient := openai.NewClient(
		option.WithAPIKey(apiKey),
	)

	params := responses.ResponseNewParams{
		Model: openai.ChatModelGPT4oMini,
		Input: responses.ResponseNewParamsInputUnion{
			OfString: openai.String(string(tripDataBytes)),
		},
	}

	resp, err := oaiClient.Responses.New(context.Background(), params)
	if err != nil {
		return err
	}

	return e.JSON(http.StatusOK, resp)
}
