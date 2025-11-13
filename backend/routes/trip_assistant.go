package routes

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"sort"
	"strings"
	"time"

	"github.com/pocketbase/dbx"
	"github.com/pocketbase/pocketbase/core"
	pbtypes "github.com/pocketbase/pocketbase/tools/types"
)

type assistantMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type tripAssistantRequest struct {
	Messages []assistantMessage `json:"messages"`
}

type tripAssistantResponse struct {
	Message assistantMessage `json:"message"`
}

type tripAssistantContext struct {
	Trip            basicTrip               `json:"trip"`
	Notes           string                  `json:"notes,omitempty"`
	Destinations    []tripDestination       `json:"destinations,omitempty"`
	Participants    []tripParticipant       `json:"participants,omitempty"`
	Budget          *costSummary            `json:"budget,omitempty"`
	Transportations []transportationSummary `json:"transportations,omitempty"`
	Lodgings        []lodgingSummary        `json:"lodgings,omitempty"`
	Activities      []activitySummary       `json:"activities,omitempty"`
	GeneratedAt     string                  `json:"generatedAt"`
}

type basicTrip struct {
	Id          string `json:"id"`
	Name        string `json:"name"`
	Description string `json:"description"`
	StartDate   string `json:"startDate"`
	EndDate     string `json:"endDate"`
}

type tripDestination struct {
	Name        string `json:"name"`
	Country     string `json:"country,omitempty"`
	State       string `json:"state,omitempty"`
	Timezone    string `json:"timezone,omitempty"`
	Latitude    string `json:"latitude,omitempty"`
	Longitude   string `json:"longitude,omitempty"`
	Category    string `json:"category,omitempty"`
	Description string `json:"description,omitempty"`
}

type tripParticipant struct {
	Name  string `json:"name"`
	Email string `json:"email,omitempty"`
}

type costSummary struct {
	Value    float64 `json:"value"`
	Currency string  `json:"currency"`
}

type transportationSummary struct {
	Id          string                 `json:"id"`
	Type        string                 `json:"type"`
	Origin      string                 `json:"origin"`
	Destination string                 `json:"destination"`
	Departure   string                 `json:"departure"`
	Arrival     string                 `json:"arrival,omitempty"`
	Cost        *costSummary           `json:"cost,omitempty"`
	Metadata    map[string]interface{} `json:"metadata,omitempty"`
	Notes       string                 `json:"notes,omitempty"`
}

type lodgingSummary struct {
	Id            string                 `json:"id"`
	Type          string                 `json:"type"`
	Name          string                 `json:"name"`
	Address       string                 `json:"address,omitempty"`
	CheckIn       string                 `json:"checkIn"`
	CheckOut      string                 `json:"checkOut"`
	Confirmation  string                 `json:"confirmation,omitempty"`
	Cost          *costSummary           `json:"cost,omitempty"`
	Metadata      map[string]interface{} `json:"metadata,omitempty"`
	ReservationBy string                 `json:"reservationBy,omitempty"`
}

type activitySummary struct {
	Id          string                 `json:"id"`
	Name        string                 `json:"name"`
	Description string                 `json:"description,omitempty"`
	Address     string                 `json:"address,omitempty"`
	Start       string                 `json:"start"`
	End         string                 `json:"end,omitempty"`
	Cost        *costSummary           `json:"cost,omitempty"`
	Metadata    map[string]interface{} `json:"metadata,omitempty"`
}

type responsesAPIResponse struct {
	OutputText []string               `json:"output_text"`
	Output     []responsesAPIMessage  `json:"output"`
}

type responsesAPIMessage struct {
	Role    string                     `json:"role"`
	Content []responsesAPIContentBlock `json:"content"`
}

type responsesAPIContentBlock struct {
	Type string `json:"type"`
	Text string `json:"text"`
}

const (
	openAIResponsesEndpoint = "https://api.openai.com/v1/responses"
	openAIModel             = "gpt-5-mini"
)

func TripAssistant(e *core.RequestEvent) error {
	apiKey := strings.TrimSpace(os.Getenv("OPENAI_API_KEY"))
	if apiKey == "" {
		return e.JSON(http.StatusServiceUnavailable, map[string]string{
			"error": "OPENAI_API_KEY is not configured on the server",
		})
	}

	var req tripAssistantRequest
	if err := json.NewDecoder(e.Request.Body).Decode(&req); err != nil {
		return e.JSON(http.StatusBadRequest, map[string]string{
			"error": "invalid request body",
		})
	}

	if len(req.Messages) == 0 {
		return e.JSON(http.StatusBadRequest, map[string]string{
			"error": "at least one message is required",
		})
	}

	tripVal := e.Get("trip")
	if tripVal == nil {
		return e.JSON(http.StatusBadRequest, map[string]string{
			"error": "trip context is missing",
		})
	}

	tripRecord, ok := tripVal.(*core.Record)
	if !ok {
		return e.JSON(http.StatusBadRequest, map[string]string{
			"error": "unable to read trip info",
		})
	}

	ctx, err := buildTripAssistantContext(e.App, tripRecord)
	if err != nil {
		e.App.Logger().Error("TripAssistant build context error", "error", err, "tripId", tripRecord.Id)
		return e.JSON(http.StatusInternalServerError, map[string]string{
			"error": "unable to load the latest trip context",
		})
	}

	responseInput, err := buildResponsesInput(req.Messages, ctx)
	if err != nil {
		e.App.Logger().Error("TripAssistant failed to build input", "error", err, "tripId", tripRecord.Id)
		return e.JSON(http.StatusInternalServerError, map[string]string{
			"error": "could not format the assistant request",
		})
	}

	reply, err := invokeResponsesAPI(e.Request.Context(), apiKey, responseInput)
	if err != nil {
		e.App.Logger().Error("TripAssistant call failed", "error", err, "tripId", tripRecord.Id)
		return e.JSON(http.StatusBadGateway, map[string]string{
			"error": fmt.Sprintf("assistant request failed: %s", err.Error()),
		})
	}

	return e.JSON(http.StatusOK, tripAssistantResponse{
		Message: assistantMessage{
			Role:    "assistant",
			Content: reply,
		},
	})
}

func buildTripAssistantContext(app core.App, trip *core.Record) (*tripAssistantContext, error) {
	destinations := parseDestinations(app, trip)
	participants := parseParticipants(app, trip)

	ctx := &tripAssistantContext{
		Trip: basicTrip{
			Id:          trip.Id,
			Name:        trip.GetString("name"),
			Description: trip.GetString("description"),
			StartDate:   formatDate(trip.GetDateTime("startDate")),
			EndDate:     formatDate(trip.GetDateTime("endDate")),
		},
		Notes:        trip.GetString("notes"),
		Destinations: destinations,
		Participants: participants,
		GeneratedAt:  time.Now().UTC().Format(time.RFC3339),
	}

	if ctx.Notes == "" {
		ctx.Notes = trip.GetString("description")
	}

	var budget costSummary
	if err := trip.UnmarshalJSONField("budget", &budget); err == nil {
		if budget.Value != 0 || budget.Currency != "" {
			ctx.Budget = &budget
		}
	}

	transportations, err := collectTransportations(app, trip)
	if err != nil {
		return nil, err
	}
	ctx.Transportations = transportations

	lodgings, err := collectLodgings(app, trip)
	if err != nil {
		return nil, err
	}
	ctx.Lodgings = lodgings

	activities, err := collectActivities(app, trip)
	if err != nil {
		return nil, err
	}
	ctx.Activities = activities

	return ctx, nil
}

func collectTransportations(app core.App, trip *core.Record) ([]transportationSummary, error) {
	records, err := app.FindAllRecords("transportations", dbx.NewExp("trip = {:tripId}", dbx.Params{"tripId": trip.Id}))
	if err != nil {
		return nil, err
	}

	sort.Slice(records, func(i, j int) bool {
		return records[i].GetDateTime("departureTime").Time().Before(records[j].GetDateTime("departureTime").Time())
	})

	summaries := make([]transportationSummary, 0, len(records))
	for _, record := range records {
		var cost costSummary
		var metadata map[string]interface{}

		_ = record.UnmarshalJSONField("cost", &cost)
		_ = record.UnmarshalJSONField("metadata", &metadata)

		entry := transportationSummary{
			Id:          record.Id,
			Type:        record.GetString("type"),
			Origin:      record.GetString("origin"),
			Destination: record.GetString("destination"),
			Departure:   formatDate(record.GetDateTime("departureTime")),
			Arrival:     formatDate(record.GetDateTime("arrivalTime")),
			Notes:       record.GetString("notes"),
		}

		if cost.Value != 0 || cost.Currency != "" {
			entry.Cost = &cost
		}
		if len(metadata) > 0 {
			entry.Metadata = metadata
		}

		summaries = append(summaries, entry)
	}

	return summaries, nil
}

func collectLodgings(app core.App, trip *core.Record) ([]lodgingSummary, error) {
	records, err := app.FindAllRecords("lodgings", dbx.NewExp("trip = {:tripId}", dbx.Params{"tripId": trip.Id}))
	if err != nil {
		return nil, err
	}

	sort.Slice(records, func(i, j int) bool {
		return records[i].GetDateTime("startDate").Time().Before(records[j].GetDateTime("startDate").Time())
	})

	summaries := make([]lodgingSummary, 0, len(records))
	for _, record := range records {
		var cost costSummary
		var metadata map[string]interface{}

		_ = record.UnmarshalJSONField("cost", &cost)
		_ = record.UnmarshalJSONField("metadata", &metadata)

		entry := lodgingSummary{
			Id:           record.Id,
			Type:         record.GetString("type"),
			Name:         record.GetString("name"),
			Address:      record.GetString("address"),
			CheckIn:      formatDate(record.GetDateTime("startDate")),
			CheckOut:     formatDate(record.GetDateTime("endDate")),
			Confirmation: record.GetString("confirmationCode"),
		}

		if resBy := record.GetString("reservationName"); resBy != "" {
			entry.ReservationBy = resBy
		}

		if cost.Value != 0 || cost.Currency != "" {
			entry.Cost = &cost
		}
		if len(metadata) > 0 {
			entry.Metadata = metadata
		}

		summaries = append(summaries, entry)
	}

	return summaries, nil
}

func collectActivities(app core.App, trip *core.Record) ([]activitySummary, error) {
	records, err := app.FindAllRecords("activities", dbx.NewExp("trip = {:tripId}", dbx.Params{"tripId": trip.Id}))
	if err != nil {
		return nil, err
	}

	sort.Slice(records, func(i, j int) bool {
		return records[i].GetDateTime("startDate").Time().Before(records[j].GetDateTime("startDate").Time())
	})

	summaries := make([]activitySummary, 0, len(records))
	for _, record := range records {
		var cost costSummary
		var metadata map[string]interface{}

		_ = record.UnmarshalJSONField("cost", &cost)
		_ = record.UnmarshalJSONField("metadata", &metadata)

		entry := activitySummary{
			Id:          record.Id,
			Name:        record.GetString("name"),
			Description: record.GetString("description"),
			Address:     record.GetString("address"),
			Start:       formatDate(record.GetDateTime("startDate")),
			End:         formatDate(record.GetDateTime("endDate")),
		}

		if cost.Value != 0 || cost.Currency != "" {
			entry.Cost = &cost
		}
		if len(metadata) > 0 {
			entry.Metadata = metadata
		}

		summaries = append(summaries, entry)
	}

	return summaries, nil
}

func parseDestinations(app core.App, trip *core.Record) []tripDestination {
	data := trip.GetString("destinations")
	if strings.TrimSpace(data) == "" {
		return nil
	}

	var raw []map[string]interface{}
	if err := json.Unmarshal([]byte(data), &raw); err != nil {
		app.Logger().Warn("Unable to parse trip destinations", "error", err, "tripId", trip.Id)
		return nil
	}

	results := make([]tripDestination, 0, len(raw))
	for _, d := range raw {
		results = append(results, tripDestination{
			Name:        stringValue(d["name"]),
			Country:     stringValue(d["countryName"]),
			State:       stringValue(d["stateName"]),
			Timezone:    stringValue(d["timezone"]),
			Latitude:    stringValue(d["latitude"]),
			Longitude:   stringValue(d["longitude"]),
			Category:    stringValue(d["category"]),
			Description: stringValue(d["description"]),
		})
	}
	return results
}

func parseParticipants(app core.App, trip *core.Record) []tripParticipant {
	data := trip.GetString("participants")
	if strings.TrimSpace(data) == "" {
		return nil
	}

	var raw []map[string]interface{}
	if err := json.Unmarshal([]byte(data), &raw); err != nil {
		app.Logger().Warn("Unable to parse trip participants", "error", err, "tripId", trip.Id)
		return nil
	}

	results := make([]tripParticipant, 0, len(raw))
	for _, p := range raw {
		results = append(results, tripParticipant{
			Name:  stringValue(p["name"]),
			Email: stringValue(p["email"]),
		})
	}
	return results
}

func stringValue(v interface{}) string {
	if v == nil {
		return ""
	}
	if s, ok := v.(string); ok {
		return s
	}
	return fmt.Sprintf("%v", v)
}

func formatDate(dt pbtypes.DateTime) string {
	if dt.IsZero() {
		return ""
	}
	return dt.Time().UTC().Format(time.RFC3339)
}

func buildResponsesInput(messages []assistantMessage, ctx *tripAssistantContext) ([]map[string]interface{}, error) {
	ctxJSON, err := json.MarshalIndent(ctx, "", "  ")
	if err != nil {
		return nil, err
	}

	systemPrompt := "You are Surmai's AI-powered itinerary assistant. Use the trip context to answer questions, reference actual plans, and offer proactive suggestions when helpful. Keep answers concise, organized, and grounded in the provided data unless the user explicitly asks for speculation."
	contextPrompt := fmt.Sprintf("Latest trip context:\n%s", string(ctxJSON))

	input := []map[string]interface{}{
		newResponsesTextBlock("system", systemPrompt),
		newResponsesTextBlock("system", contextPrompt),
	}

	for _, message := range truncateConversation(messages, 20) {
		if message.Content == "" {
			continue
		}
		role := message.Role
		if role != "user" && role != "assistant" {
			continue
		}
		input = append(input, newResponsesTextBlock(role, message.Content))
	}

	return input, nil
}

func truncateConversation(messages []assistantMessage, limit int) []assistantMessage {
	if len(messages) <= limit {
		return messages
	}
	return messages[len(messages)-limit:]
}

func newResponsesTextBlock(role, text string) map[string]interface{} {
	return map[string]interface{}{
		"role": role,
		"content": []map[string]string{
			{
				"type": "input_text",
				"text": text,
			},
		},
	}
}

func invokeResponsesAPI(ctx context.Context, apiKey string, input []map[string]interface{}) (string, error) {
	payload := map[string]interface{}{
		"model":       openAIModel,
		"input":       input,
	}

	body, err := json.Marshal(payload)
	if err != nil {
		return "", err
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, openAIResponsesEndpoint, bytes.NewReader(body))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+apiKey)

	client := &http.Client{
		Timeout: 45 * time.Second,
	}

	resp, err := client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 400 {
		return "", parseOpenAIError(resp)
	}

	var response responsesAPIResponse
	if err := json.NewDecoder(resp.Body).Decode(&response); err != nil {
		return "", err
	}

	text := strings.TrimSpace(strings.Join(response.OutputText, "\n"))
	if text == "" {
		text = extractFallbackOutput(response)
	}
	if text == "" {
		return "", errors.New("assistant returned an empty message")
	}

	return text, nil
}

func parseOpenAIError(resp *http.Response) error {
	data, err := io.ReadAll(resp.Body)
	if err != nil || len(data) == 0 {
		return fmt.Errorf("openai api error: %s", resp.Status)
	}

	var payload map[string]interface{}
	if err := json.Unmarshal(data, &payload); err != nil {
		return fmt.Errorf("openai api error: %s", resp.Status)
	}

	if errField, ok := payload["error"].(map[string]interface{}); ok {
		msg := stringValue(errField["message"])
		if msg != "" {
			return errors.New(msg)
		}
	}

	return fmt.Errorf("openai api error: %s", resp.Status)
}

func extractFallbackOutput(response responsesAPIResponse) string {
	for _, message := range response.Output {
		for _, block := range message.Content {
			if block.Type == "output_text" && strings.TrimSpace(block.Text) != "" {
				return strings.TrimSpace(block.Text)
			}
		}
	}
	return ""
}
