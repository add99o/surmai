package routes

import (
	bt "backend/types"
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"sort"
	"strings"
	"time"

	"github.com/pocketbase/dbx"
	"github.com/pocketbase/pocketbase/core"
	"golang.org/x/text/cases"
	"golang.org/x/text/language"
)

type aiChatMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type aiChatRequest struct {
	Messages []aiChatMessage `json:"messages"`
}

type openAIContent struct {
	Type string `json:"type"`
	Text string `json:"text"`
}

const (
openAIInputTextType  = "input_text"
openAIOutputTextType = "output_text"
)

type openAIInput struct {
	Role    string          `json:"role"`
	Content []openAIContent `json:"content"`
}

type openAIRequestPayload struct {
	Model           string        `json:"model"`
	Input           []openAIInput `json:"input"`
	MaxOutputTokens int           `json:"max_output_tokens,omitempty"`
	Temperature     float64       `json:"temperature,omitempty"`
	Modalities      []string      `json:"modalities,omitempty"`
}

type openAIResponsePayload struct {
	Output []struct {
		Role    string `json:"role"`
		Content []struct {
			Type string `json:"type"`
			Text string `json:"text"`
		} `json:"content"`
	} `json:"output"`
	Error *struct {
		Message string `json:"message"`
	} `json:"error"`
}

type itineraryItem struct {
	Start       time.Time
	Description string
}

const (
	aiSystemPrompt = "You are Surmai's AI travel concierge. Use the live itinerary, trip logistics, and budget context provided to answer traveler questions with concrete suggestions. When you make recommendations cite the specific dates, locations, or reservations that already exist in the plan and avoid inventing details that are not in the itinerary."
	maxAiMessages  = 12
)

func ChatAboutTripItinerary(e *core.RequestEvent) error {
	apiKey := os.Getenv("OPENAI_API_KEY")
	if apiKey == "" {
		return e.JSON(http.StatusServiceUnavailable, map[string]string{
			"error": "OpenAI integration is not configured.",
		})
	}

	var chatReq aiChatRequest
	if err := json.NewDecoder(e.Request.Body).Decode(&chatReq); err != nil {
		return e.BadRequestError("invalid chat payload", err)
	}

	cleanedMessages := sanitizeMessages(chatReq.Messages)
	if len(cleanedMessages) == 0 {
		return e.BadRequestError("at least one user message is required", nil)
	}

	trip := e.Get("trip").(*core.Record)

	contextSummary, err := buildTripContext(e, trip)
	if err != nil {
		return err
	}

	systemInput := openAIInput{
		Role: "system",
		Content: []openAIContent{
			{Type: openAIInputTextType, Text: fmt.Sprintf("%s\n\nTrip data snapshot:\n%s", aiSystemPrompt, contextSummary)},
		},
	}

	payload := openAIRequestPayload{
		Model:       "gpt-5-mini",
		Input:       append([]openAIInput{systemInput}, cleanedMessages...),
		Temperature: 0.2,
		Modalities:  []string{"text"},
	}

	if len(cleanedMessages) > 0 {
		payload.MaxOutputTokens = 800
	}

	body, err := json.Marshal(payload)
	if err != nil {
		return err
	}

	request, err := http.NewRequest(http.MethodPost, "https://api.openai.com/v1/responses", bytes.NewReader(body))
	if err != nil {
		return err
	}

	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Authorization", fmt.Sprintf("Bearer %s", apiKey))

	client := &http.Client{Timeout: 60 * time.Second}
	resp, err := client.Do(request)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 400 {
		data, _ := io.ReadAll(resp.Body)
		e.App.Logger().Error("openai request failed", "status", resp.StatusCode, "body", string(data))
		return e.JSON(http.StatusBadGateway, map[string]string{"error": "OpenAI request failed"})
	}

	var aiResp openAIResponsePayload
	if err := json.NewDecoder(resp.Body).Decode(&aiResp); err != nil {
		return err
	}

	if aiResp.Error != nil {
		e.App.Logger().Error("openai error", "message", aiResp.Error.Message)
		return e.JSON(http.StatusBadGateway, map[string]string{"error": aiResp.Error.Message})
	}

	reply := extractAssistantReply(aiResp)
	if reply == "" {
		reply = "I'm sorry, I couldn't generate a response right now. Please try again."
	}

	return e.JSON(http.StatusOK, map[string]string{"reply": reply})
}

func sanitizeMessages(messages []aiChatMessage) []openAIInput {
	if len(messages) > maxAiMessages {
		messages = messages[len(messages)-maxAiMessages:]
	}

	inputs := make([]openAIInput, 0, len(messages))
	for _, message := range messages {
		text := strings.TrimSpace(message.Content)
		if text == "" {
			continue
		}

		role := "user"
		if message.Role == "assistant" {
			role = "assistant"
		}

		inputs = append(inputs, openAIInput{
			Role:    role,
			Content: []openAIContent{{Type: openAIInputTextType, Text: text}},
		})
	}

	return inputs
}

func extractAssistantReply(resp openAIResponsePayload) string {
	var builder strings.Builder
	for _, output := range resp.Output {
		if output.Role != "assistant" {
			continue
		}
		for _, content := range output.Content {
			if content.Type == openAIOutputTextType || content.Type == "text" {
				builder.WriteString(content.Text)
			}
		}
	}
	return strings.TrimSpace(builder.String())
}

func buildTripContext(e *core.RequestEvent, trip *core.Record) (string, error) {
	var builder strings.Builder

	start := trip.GetDateTime("startDate").Time()
	end := trip.GetDateTime("endDate").Time()
	fmt.Fprintf(&builder, "Trip: %s (%s - %s)\n", trip.GetString("name"), start.Format(time.RFC1123), end.Format(time.RFC1123))

	description := strings.TrimSpace(trip.GetString("description"))
	if description != "" {
		fmt.Fprintf(&builder, "Description: %s\n", description)
	}

	if budget := parseBudget(trip); budget != "" {
		fmt.Fprintf(&builder, "Budget: %s\n", budget)
	}

	if destinations := parseDestinations(trip); destinations != "" {
		fmt.Fprintf(&builder, "Destinations: %s\n", destinations)
	}

	if participants := parseParticipants(trip); participants != "" {
		fmt.Fprintf(&builder, "Travelers: %s\n", participants)
	}

	if notes := strings.TrimSpace(trip.GetString("notes")); notes != "" {
		fmt.Fprintf(&builder, "Internal notes: %s\n", truncate(notes, 800))
	}

	items, err := buildItineraryItems(e, trip.Id)
	if err != nil {
		return "", err
	}

	if len(items) == 0 {
		builder.WriteString("No detailed itinerary entries were found.\n")
	} else {
		builder.WriteString("Detailed timeline:\n")
		for _, item := range items {
			fmt.Fprintf(&builder, "- %s — %s\n", item.Start.Format(time.RFC1123), item.Description)
		}
	}

	return builder.String(), nil
}

func buildItineraryItems(e *core.RequestEvent, tripId string) ([]itineraryItem, error) {
	items := make([]itineraryItem, 0)

	transportations, err := e.App.FindAllRecords("transportations", dbx.NewExp("trip = {:tripId}", dbx.Params{"tripId": tripId}))
	if err != nil {
		return nil, err
	}

	for _, tr := range transportations {
		departure := tr.GetDateTime("departureTime")
		if departure.IsZero() {
			continue
		}
		items = append(items, itineraryItem{
			Start:       departure.Time(),
			Description: formatTransportation(tr),
		})
	}

	lodgings, err := e.App.FindAllRecords("lodgings", dbx.NewExp("trip = {:tripId}", dbx.Params{"tripId": tripId}))
	if err != nil {
		return nil, err
	}

	for _, lodging := range lodgings {
		checkIn := lodging.GetDateTime("startDate")
		if checkIn.IsZero() {
			continue
		}
		items = append(items, itineraryItem{
			Start:       checkIn.Time(),
			Description: formatLodging(lodging),
		})
	}

	activities, err := e.App.FindAllRecords("activities", dbx.NewExp("trip = {:tripId}", dbx.Params{"tripId": tripId}))
	if err != nil {
		return nil, err
	}

	for _, activity := range activities {
		start := activity.GetDateTime("startDate")
		if start.IsZero() {
			continue
		}
		items = append(items, itineraryItem{
			Start:       start.Time(),
			Description: formatActivity(activity),
		})
	}

	sort.Slice(items, func(i, j int) bool {
		return items[i].Start.Before(items[j].Start)
	})

	return items, nil
}

func formatTransportation(record *core.Record) string {
	var metadata map[string]any
	_ = record.UnmarshalJSONField("metadata", &metadata)

	var cost bt.Cost
	_ = record.UnmarshalJSONField("cost", &cost)

	builder := &strings.Builder{}
	fmt.Fprintf(builder, "%s from %s to %s", titleize(record.GetString("type")), record.GetString("origin"), record.GetString("destination"))

	departure := record.GetDateTime("departureTime").Time()
	arrival := record.GetDateTime("arrivalTime").Time()
	if !departure.IsZero() && !arrival.IsZero() {
		fmt.Fprintf(builder, " (departing %s arriving %s)", departure.Format(time.RFC1123), arrival.Format(time.RFC1123))
	}

	if provider, ok := metadata["provider"]; ok {
		switch value := provider.(type) {
		case string:
			if value != "" {
				fmt.Fprintf(builder, ". Provider: %s", value)
			}
		case map[string]any:
			if name, ok := value["name"].(string); ok && name != "" {
				fmt.Fprintf(builder, ". Provider: %s", name)
			}
		}
	}

	if reservation, ok := metadata["reservation"].(string); ok && reservation != "" {
		fmt.Fprintf(builder, ". Confirmation: %s", reservation)
	}

	if flightNumber, ok := metadata["flightNumber"].(string); ok && flightNumber != "" {
		fmt.Fprintf(builder, ". Flight: %s", strings.ToUpper(flightNumber))
	}

	if originAddress, ok := metadata["originAddress"].(string); ok && originAddress != "" {
		fmt.Fprintf(builder, ". Origin details: %s", originAddress)
	}

	if destinationAddress, ok := metadata["destinationAddress"].(string); ok && destinationAddress != "" {
		fmt.Fprintf(builder, ". Destination details: %s", destinationAddress)
	}

	if cost.Currency != "" && cost.Value != 0 {
		fmt.Fprintf(builder, ". Cost: %.2f %s", cost.Value, cost.Currency)
	}

	return builder.String()
}

func formatLodging(record *core.Record) string {
	var cost bt.Cost
	_ = record.UnmarshalJSONField("cost", &cost)

	builder := &strings.Builder{}
	fmt.Fprintf(builder, "%s stay at %s", titleize(record.GetString("type")), record.GetString("name"))
	if address := strings.TrimSpace(record.GetString("address")); address != "" {
		fmt.Fprintf(builder, " (%s)", address)
	}

	start := record.GetDateTime("startDate").Time()
	end := record.GetDateTime("endDate").Time()
	if !start.IsZero() {
		fmt.Fprintf(builder, " from %s", start.Format(time.RFC1123))
	}
	if !end.IsZero() {
		fmt.Fprintf(builder, " to %s", end.Format(time.RFC1123))
	}

	if code := strings.TrimSpace(record.GetString("confirmationCode")); code != "" {
		fmt.Fprintf(builder, ". Confirmation: %s", code)
	}

	if cost.Currency != "" && cost.Value != 0 {
		fmt.Fprintf(builder, ". Cost: %.2f %s", cost.Value, cost.Currency)
	}

	return builder.String()
}

func formatActivity(record *core.Record) string {
	var cost bt.Cost
	_ = record.UnmarshalJSONField("cost", &cost)

	builder := &strings.Builder{}
	fmt.Fprintf(builder, "Activity %s", record.GetString("name"))
	if location := strings.TrimSpace(record.GetString("address")); location != "" {
		fmt.Fprintf(builder, " at %s", location)
	}

	start := record.GetDateTime("startDate").Time()
	end := record.GetDateTime("endDate").Time()
	if !start.IsZero() {
		fmt.Fprintf(builder, " starting %s", start.Format(time.RFC1123))
	}
	if !end.IsZero() {
		fmt.Fprintf(builder, " ending %s", end.Format(time.RFC1123))
	}

	if cost.Currency != "" && cost.Value != 0 {
		fmt.Fprintf(builder, ". Cost: %.2f %s", cost.Value, cost.Currency)
	}

	description := strings.TrimSpace(record.GetString("description"))
	if description != "" {
		fmt.Fprintf(builder, ". Notes: %s", truncate(description, 240))
	}

	return builder.String()
}

func parseDestinations(trip *core.Record) string {
	var destinations []bt.Destination
	if err := trip.UnmarshalJSONField("destinations", &destinations); err != nil {
		return ""
	}

	names := make([]string, 0, len(destinations))
	for _, destination := range destinations {
		nameParts := []string{destination.Name}
		if destination.StateName != "" {
			nameParts = append(nameParts, destination.StateName)
		}
		if destination.CountryName != "" {
			nameParts = append(nameParts, destination.CountryName)
		}
		names = append(names, strings.Join(nameParts, ", "))
	}

	return strings.Join(names, " | ")
}

func parseParticipants(trip *core.Record) string {
	var participants []bt.Participant
	if err := trip.UnmarshalJSONField("participants", &participants); err != nil {
		return ""
	}

	names := make([]string, 0, len(participants))
	for _, participant := range participants {
		if participant.Name != "" {
			names = append(names, participant.Name)
		}
	}

	return strings.Join(names, ", ")
}

func parseBudget(trip *core.Record) string {
	var cost bt.Cost
	if err := trip.UnmarshalJSONField("budget", &cost); err != nil {
		return ""
	}

	if cost.Currency == "" || cost.Value == 0 {
		return ""
	}

	return fmt.Sprintf("%.2f %s", cost.Value, cost.Currency)
}

func truncate(value string, max int) string {
	value = strings.TrimSpace(value)
	if len(value) <= max {
		return value
	}
	return value[:max] + "…"
}

var titleCase = cases.Title(language.English)

func titleize(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return ""
	}
	replaced := strings.ReplaceAll(value, "_", " ")
	return titleCase.String(replaced)
}
