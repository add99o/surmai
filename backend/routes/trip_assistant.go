package routes

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
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
	OutputText []string              `json:"output_text"`
	Output     []responsesAPIMessage `json:"output"`
}

type responsesAPIMessage struct {
	Role    string                     `json:"role"`
	Content []responsesAPIContentBlock `json:"content"`
}

type responsesAPIContentBlock struct {
	Type string `json:"type"`
	Text string `json:"text"`
}

const proposalTTL = 2 * time.Minute

const (
	assistantToolCreateActivity       = "create_activity"
	assistantToolCreateLodging        = "create_lodging"
	assistantToolCreateTransportation = "create_transportation"

	assistantToolUpdateActivity       = "update_activity"
	assistantToolUpdateLodging        = "update_lodging"
	assistantToolUpdateTransportation = "update_transportation"

	assistantToolDeleteActivity       = "delete_activity"
	assistantToolDeleteLodging        = "delete_lodging"
	assistantToolDeleteTransportation = "delete_transportation"
)

type assistantProposal struct {
	ID        string
	TripID    string
	Tool      string
	Arguments map[string]interface{}
	ExpiresAt time.Time
	CreatedAt time.Time
}

var proposalStore = struct {
	sync.RWMutex
	items map[string]*assistantProposal
}{
	items: make(map[string]*assistantProposal),
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

func TripAssistantStream(e *core.RequestEvent) error {
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
		e.App.Logger().Error("TripAssistant stream build context error", "error", err, "tripId", tripRecord.Id)
		return e.JSON(http.StatusInternalServerError, map[string]string{
			"error": "unable to load the latest trip context",
		})
	}

	responseInput, err := buildResponsesInput(req.Messages, ctx)
	if err != nil {
		e.App.Logger().Error("TripAssistant stream failed to build input", "error", err, "tripId", tripRecord.Id)
		return e.JSON(http.StatusInternalServerError, map[string]string{
			"error": "could not format the assistant request",
		})
	}

	flusher, ok := e.Response.(http.Flusher)
	if !ok {
		return e.JSON(http.StatusInternalServerError, map[string]string{
			"error": "streaming is not supported on this server",
		})
	}

	writer := e.Response
	writer.Header().Set("Content-Type", "text/event-stream")
	writer.Header().Set("Cache-Control", "no-cache")
	writer.Header().Set("Connection", "keep-alive")

	if err := streamResponsesToClient(e.Request.Context(), writer, flusher, apiKey, tripRecord.Id, responseInput); err != nil {
		e.App.Logger().Error("TripAssistant stream failed", "error", err, "tripId", tripRecord.Id)
		sendSSEEvent(writer, flusher, map[string]string{
			"type":    "error",
			"message": "assistant request failed",
		})
	}

	return nil
}

type proposalDecisionRequest struct {
	Decision string `json:"decision"`
}

func AssistantProposalDecision(e *core.RequestEvent) error {
	tripVal := e.Get("trip")
	if tripVal == nil {
		return e.JSON(http.StatusBadRequest, map[string]string{"error": "trip context missing"})
	}
	tripRecord := tripVal.(*core.Record)

	proposalID := e.Request.PathValue("proposalId")
	if proposalID == "" {
		return e.JSON(http.StatusBadRequest, map[string]string{"error": "proposal id missing"})
	}

	var req proposalDecisionRequest
	if err := json.NewDecoder(e.Request.Body).Decode(&req); err != nil {
		return e.JSON(http.StatusBadRequest, map[string]string{"error": "invalid body"})
	}

	proposal, ok := getAssistantProposal(proposalID)
	if !ok {
		return e.JSON(http.StatusGone, map[string]string{"error": "proposal expired"})
	}

	if proposal.TripID != tripRecord.Id {
		return e.JSON(http.StatusForbidden, map[string]string{"error": "proposal does not belong to this trip"})
	}

	if proposal.expired() {
		popAssistantProposal(proposalID)
		return e.JSON(http.StatusGone, map[string]string{"error": "proposal timed out"})
	}

	switch strings.ToLower(req.Decision) {
	case "approve":
		message, err := applyAssistantProposal(e.App, tripRecord, proposal)
		if err != nil {
			return e.JSON(http.StatusInternalServerError, map[string]string{"error": err.Error()})
		}
		popAssistantProposal(proposalID)
		return e.JSON(http.StatusOK, map[string]string{
			"status":  "approved",
			"message": message,
		})
	case "decline":
		popAssistantProposal(proposalID)
		return e.JSON(http.StatusOK, map[string]string{
			"status":  "declined",
			"message": "Okay, I will skip that change.",
		})
	case "timeout":
		popAssistantProposal(proposalID)
		return e.JSON(http.StatusOK, map[string]string{
			"status":  "timeout",
			"message": "The request expired. Ask again if you'd like me to re-create it.",
		})
	default:
		return e.JSON(http.StatusBadRequest, map[string]string{"error": "decision must be approve, decline, or timeout"})
	}
}

func applyAssistantProposal(app core.App, trip *core.Record, proposal *assistantProposal) (string, error) {
	switch proposal.Tool {
	case assistantToolCreateActivity:
		return saveActivityProposal(app, trip.Id, proposal.Arguments)
	case assistantToolUpdateActivity:
		return updateActivityProposal(app, trip.Id, proposal.Arguments)
	case assistantToolDeleteActivity:
		return deleteActivityProposal(app, trip.Id, proposal.Arguments)
	case assistantToolCreateLodging:
		return saveLodgingProposal(app, trip.Id, proposal.Arguments)
	case assistantToolUpdateLodging:
		return updateLodgingProposal(app, trip.Id, proposal.Arguments)
	case assistantToolDeleteLodging:
		return deleteLodgingProposal(app, trip.Id, proposal.Arguments)
	case assistantToolCreateTransportation:
		return saveTransportationProposal(app, trip.Id, proposal.Arguments)
	case assistantToolUpdateTransportation:
		return updateTransportationProposal(app, trip.Id, proposal.Arguments)
	case assistantToolDeleteTransportation:
		return deleteTransportationProposal(app, trip.Id, proposal.Arguments)
	default:
		return "", errors.New("unsupported proposal type")
	}
}

func saveActivityProposal(app core.App, tripID string, args map[string]interface{}) (string, error) {
	collection, err := app.FindCollectionByNameOrId("activities")
	if err != nil {
		return "", err
	}

	record := core.NewRecord(collection)
	record.Set("trip", tripID)
	record.Set("name", stringValue(args["name"]))
	record.Set("description", stringValue(args["description"]))
	record.Set("address", stringValue(args["address"]))
	record.Set("notes", stringValue(args["notes"]))

	if start := stringValue(args["start_time"]); start != "" {
		record.Set("startDate", start)
	}
	if end := stringValue(args["end_time"]); end != "" {
		record.Set("endDate", end)
	}

	costValue := floatValue(args["cost_value"])
	currency := stringValue(args["cost_currency"])
	if costValue > 0 && currency != "" {
		costPayload := map[string]interface{}{
			"value":    costValue,
			"currency": currency,
		}
		record.Set("cost", costPayload)
	}

	if metadata := buildActivityMetadata(args); len(metadata) > 0 {
		record.Set("metadata", metadata)
	}

	if err := app.Save(record); err != nil {
		return "", err
	}

	return fmt.Sprintf("Added activity \"%s\" on %s.", stringValue(args["name"]), stringValue(args["start_time"])), nil
}

func updateActivityProposal(app core.App, tripID string, args map[string]interface{}) (string, error) {
	record, err := ensureTripRecord(app, "activities", stringValue(args["record_id"]), tripID)
	if err != nil {
		return "", err
	}

	if name := stringValue(args["name"]); name != "" {
		record.Set("name", name)
	}
	if desc := stringValue(args["description"]); desc != "" {
		record.Set("description", desc)
	}
	if address := stringValue(args["address"]); address != "" {
		record.Set("address", address)
	}
	if note := stringValue(args["notes"]); note != "" {
		record.Set("notes", note)
	}
	if start := stringValue(args["start_time"]); start != "" {
		record.Set("startDate", start)
	}
	if end := stringValue(args["end_time"]); end != "" {
		record.Set("endDate", end)
	}
	if metadata := buildActivityMetadata(args); len(metadata) > 0 {
		record.Set("metadata", metadata)
	}
	applyCostUpdate(record, args)

	if err := app.Save(record); err != nil {
		return "", err
	}

	return fmt.Sprintf("Updated activity \"%s\".", record.GetString("name")), nil
}

func deleteActivityProposal(app core.App, tripID string, args map[string]interface{}) (string, error) {
	record, err := ensureTripRecord(app, "activities", stringValue(args["record_id"]), tripID)
	if err != nil {
		return "", err
	}

	name := record.GetString("name")
	if err := app.Delete(record); err != nil {
		return "", err
	}

	return fmt.Sprintf("Removed activity \"%s\".", name), nil
}

func saveLodgingProposal(app core.App, tripID string, args map[string]interface{}) (string, error) {
	collection, err := app.FindCollectionByNameOrId("lodgings")
	if err != nil {
		return "", err
	}

	record := core.NewRecord(collection)
	record.Set("trip", tripID)
	record.Set("name", stringValue(args["name"]))
	record.Set("type", stringValue(args["type"]))
	record.Set("address", stringValue(args["address"]))
	record.Set("confirmationCode", stringValue(args["confirmation"]))

	if start := stringValue(args["start_time"]); start != "" {
		record.Set("startDate", start)
	}
	if end := stringValue(args["end_time"]); end != "" {
		record.Set("endDate", end)
	}

	if err := app.Save(record); err != nil {
		return "", err
	}

	return fmt.Sprintf("Added lodging \"%s\" for %s to %s.", stringValue(args["name"]), stringValue(args["start_time"]), stringValue(args["end_time"])), nil
}

func saveTransportationProposal(app core.App, tripID string, args map[string]interface{}) (string, error) {
	collection, err := app.FindCollectionByNameOrId("transportations")
	if err != nil {
		return "", err
	}

	record := core.NewRecord(collection)
	record.Set("trip", tripID)
	record.Set("type", stringValue(args["type"]))
	record.Set("provider", stringValue(args["provider"]))
	record.Set("origin", stringValue(args["origin"]))
	record.Set("destination", stringValue(args["destination"]))
	record.Set("notes", stringValue(args["notes"]))

	if dep := stringValue(args["departure_time"]); dep != "" {
		record.Set("departureTime", dep)
	}
	if arr := stringValue(args["arrival_time"]); arr != "" {
		record.Set("arrivalTime", arr)
	}

	if err := app.Save(record); err != nil {
		return "", err
	}

	return fmt.Sprintf("Added %s from %s to %s departing %s.", stringValue(args["type"]), stringValue(args["origin"]), stringValue(args["destination"]), stringValue(args["departure_time"])), nil
}

func updateLodgingProposal(app core.App, tripID string, args map[string]interface{}) (string, error) {
	record, err := ensureTripRecord(app, "lodgings", stringValue(args["record_id"]), tripID)
	if err != nil {
		return "", err
	}

	if name := stringValue(args["name"]); name != "" {
		record.Set("name", name)
	}
	if ltype := stringValue(args["type"]); ltype != "" {
		record.Set("type", ltype)
	}
	if address := stringValue(args["address"]); address != "" {
		record.Set("address", address)
	}
	if start := stringValue(args["start_time"]); start != "" {
		record.Set("startDate", start)
	}
	if end := stringValue(args["end_time"]); end != "" {
		record.Set("endDate", end)
	}
	if confirmation := stringValue(args["confirmation"]); confirmation != "" {
		record.Set("confirmationCode", confirmation)
	}
	if notes := stringValue(args["notes"]); notes != "" {
		record.Set("notes", notes)
	}

	if err := app.Save(record); err != nil {
		return "", err
	}

	return fmt.Sprintf("Updated lodging \"%s\".", record.GetString("name")), nil
}

func deleteLodgingProposal(app core.App, tripID string, args map[string]interface{}) (string, error) {
	record, err := ensureTripRecord(app, "lodgings", stringValue(args["record_id"]), tripID)
	if err != nil {
		return "", err
	}
	name := record.GetString("name")
	if err := app.Delete(record); err != nil {
		return "", err
	}
	return fmt.Sprintf("Removed lodging \"%s\".", name), nil
}

func updateTransportationProposal(app core.App, tripID string, args map[string]interface{}) (string, error) {
	record, err := ensureTripRecord(app, "transportations", stringValue(args["record_id"]), tripID)
	if err != nil {
		return "", err
	}

	if t := stringValue(args["type"]); t != "" {
		record.Set("type", t)
	}
	if provider := stringValue(args["provider"]); provider != "" {
		record.Set("provider", provider)
	}
	if origin := stringValue(args["origin"]); origin != "" {
		record.Set("origin", origin)
	}
	if destination := stringValue(args["destination"]); destination != "" {
		record.Set("destination", destination)
	}
	if dep := stringValue(args["departure_time"]); dep != "" {
		record.Set("departureTime", dep)
	}
	if arr := stringValue(args["arrival_time"]); arr != "" {
		record.Set("arrivalTime", arr)
	}
	if notes := stringValue(args["notes"]); notes != "" {
		record.Set("notes", notes)
	}

	if err := app.Save(record); err != nil {
		return "", err
	}

	return fmt.Sprintf("Updated %s on %s.", record.GetString("type"), record.GetString("departureTime")), nil
}

func deleteTransportationProposal(app core.App, tripID string, args map[string]interface{}) (string, error) {
	record, err := ensureTripRecord(app, "transportations", stringValue(args["record_id"]), tripID)
	if err != nil {
		return "", err
	}
	label := fmt.Sprintf("%s from %s to %s", record.GetString("type"), record.GetString("origin"), record.GetString("destination"))
	if err := app.Delete(record); err != nil {
		return "", err
	}
	return fmt.Sprintf("Removed %s.", label), nil
}

func buildActivityMetadata(args map[string]interface{}) map[string]interface{} {
	meta := map[string]interface{}{}

	if dest := mapValue(args["destination"]); len(dest) > 0 {
		meta["place"] = sanitizePlaceMetadata(dest)
	}

	return meta
}

func sanitizePlaceMetadata(raw map[string]interface{}) map[string]interface{} {
	place := map[string]interface{}{}
	if name := stringValue(raw["name"]); name != "" {
		place["name"] = name
	}
	if country := stringValue(raw["country"]); country != "" {
		place["countryName"] = country
	}
	if state := stringValue(raw["state"]); state != "" {
		place["stateName"] = state
	}
	if lat := stringValue(raw["latitude"]); lat != "" {
		place["latitude"] = lat
	}
	if lng := stringValue(raw["longitude"]); lng != "" {
		place["longitude"] = lng
	}
	if tz := stringValue(raw["timezone"]); tz != "" {
		place["timezone"] = tz
	}
	if cat := stringValue(raw["category"]); cat != "" {
		place["category"] = cat
	}
	if id := stringValue(raw["place_id"]); id != "" {
		place["id"] = id
	}
	return place
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

func floatValue(v interface{}) float64 {
	switch val := v.(type) {
	case float64:
		return val
	case float32:
		return float64(val)
	case int:
		return float64(val)
	case int64:
		return float64(val)
	case json.Number:
		f, _ := val.Float64()
		return f
	case string:
		f, _ := strconv.ParseFloat(val, 64)
		return f
	default:
		return 0
	}
}

func mapValue(v interface{}) map[string]interface{} {
	if v == nil {
		return nil
	}
	if m, ok := v.(map[string]interface{}); ok {
		return m
	}
	return nil
}

func ensureTripRecord(app core.App, collection, recordID, tripID string) (*core.Record, error) {
	if recordID == "" {
		return nil, errors.New("missing record id")
	}
	record, err := app.FindRecordById(collection, recordID)
	if err != nil {
		return nil, err
	}
	if record.GetString("trip") != tripID {
		return nil, errors.New("record does not belong to this trip")
	}
	return record, nil
}

func applyCostUpdate(record *core.Record, args map[string]interface{}) bool {
	valRaw, hasValue := args["cost_value"]
	curRaw, hasCurrency := args["cost_currency"]
	if !hasValue && !hasCurrency {
		return false
	}
	value := floatValue(valRaw)
	currency := stringValue(curRaw)
	if value > 0 && currency != "" {
		record.Set("cost", map[string]interface{}{
			"value":    value,
			"currency": currency,
		})
	} else {
		record.Set("cost", nil)
	}
	return true
}

func formatDate(dt pbtypes.DateTime) string {
	if dt.IsZero() {
		return ""
	}
	return dt.Time().Format("2006-01-02T15:04:05")
}

func buildResponsesInput(messages []assistantMessage, ctx *tripAssistantContext) ([]map[string]interface{}, error) {
	ctxJSON, err := json.MarshalIndent(ctx, "", "  ")
	if err != nil {
		return nil, err
	}

	systemPrompt := "You are Surmai's AI-powered itinerary assistant. Use the trip context to answer questions, reference actual plans, and offer proactive suggestions when helpful. Keep answers concise, organized, and grounded in the provided data unless the user explicitly asks for speculation. Answers given should be easy to understand, instead of using 24hr time format, opt to use 12hr time format instead with AM/PM, any times you see, edit, or add in the trip context information or new entries will read as for the user. For dates use the format MM-DD and do not include the year. When the traveler asks you to add, adjust, or remove something, call the matching function (create/update/delete activity/lodging/transportation). Always include the record_id from the trip context when editing or deleting. Never assume the change is saved until the traveler approves it, and mention any assumptions you make when inferring missing details."
	contextPrompt := fmt.Sprintf("Latest trip context:\n%s", string(ctxJSON))

	input := []map[string]interface{}{
		newResponsesTextBlock("developer", systemPrompt),
		newResponsesTextBlock("developer", contextPrompt),
	}

	for _, message := range messages {
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

func newResponsesTextBlock(role, text string) map[string]interface{} {
	contentType := "input_text"
	if role == "assistant" {
		contentType = "output_text"
	}

	return map[string]interface{}{
		"role": role,
		"content": []map[string]string{
			{
				"type": contentType,
				"text": text,
			},
		},
	}
}

func invokeResponsesAPI(ctx context.Context, apiKey string, input []map[string]interface{}) (string, error) {
	payload := map[string]interface{}{
		"model": openAIModel,
		"input": input,
		"reasoning": map[string]string{
			"effort": "low",
		},
		"text": map[string]string{
			"verbosity": "low",
		},
		"tools":       buildAssistantTools(),
		"tool_choice": "auto",
		"include":     []string{"web_search_call.action.sources"},
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

func streamResponsesToClient(
	ctx context.Context,
	writer http.ResponseWriter,
	flusher http.Flusher,
	apiKey string,
	tripID string,
	input []map[string]interface{},
) error {
	callBuffer := &functionCallBuffer{}
	proposalIssued := false

	payload := map[string]interface{}{
		"model": openAIModel,
		"input": input,
		"reasoning": map[string]string{
			"effort": "low",
		},
		"text": map[string]string{
			"verbosity": "low",
		},
		"tools":       buildAssistantTools(),
		"tool_choice": "auto",
		"include":     []string{"web_search_call.action.sources"},
		"stream":      true,
	}

	body, err := json.Marshal(payload)
	if err != nil {
		return err
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, openAIResponsesEndpoint, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+apiKey)

	client := &http.Client{
		Timeout: 0,
	}

	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 400 {
		return parseOpenAIError(resp)
	}

	scanner := bufio.NewScanner(resp.Body)
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)

	completed := false

	for scanner.Scan() {
		line := scanner.Text()
		if line == "" || !strings.HasPrefix(line, "data:") {
			continue
		}

		data := strings.TrimSpace(strings.TrimPrefix(line, "data:"))
		if data == "[DONE]" {
			break
		}

		var event map[string]interface{}
		if err := json.Unmarshal([]byte(data), &event); err != nil {
			continue
		}

		eventType, _ := event["type"].(string)
		switch eventType {
		case "response.output_item.added":
			item, _ := event["item"].(map[string]interface{})
			if item != nil {
				callBuffer.handleOutputItemAdded(item)
			}
		case "response.function_call_arguments.delta":
			callBuffer.handleArgumentsDelta(event)
		case "response.function_call_arguments.done":
			if proposalIssued {
				continue
			}
			if proposalPayload, ok := callBuffer.finalizeProposal(event, tripID); ok {
				proposalIssued = true
				sendSSEEvent(writer, flusher, proposalPayload)
				return nil
			}
		case "response.output_text.delta":
			delta, _ := event["delta"].(string)
			if delta != "" {
				sendSSEEvent(writer, flusher, map[string]string{
					"type": "delta",
					"text": delta,
				})
			}
		case "response.completed":
			sendSSEEvent(writer, flusher, map[string]string{
				"type": "done",
			})
			completed = true
		case "response.error":
			message := stringValue(event["message"])
			if message == "" {
				message = "assistant request failed"
			}
			sendSSEEvent(writer, flusher, map[string]string{
				"type":    "error",
				"message": message,
			})
		}
	}

	if err := scanner.Err(); err != nil && !errors.Is(err, io.EOF) {
		return err
	}

	if !completed && !proposalIssued {
		sendSSEEvent(writer, flusher, map[string]string{
			"type": "done",
		})
	}

	return nil
}

func sendSSEEvent(writer http.ResponseWriter, flusher http.Flusher, payload interface{}) {
	data, err := json.Marshal(payload)
	if err != nil {
		return
	}

	_, _ = writer.Write([]byte("data: "))
	_, _ = writer.Write(data)
	_, _ = writer.Write([]byte("\n\n"))
	flusher.Flush()
}

func buildAssistantTools() []map[string]interface{} {
	tools := []map[string]interface{}{
		{
			"type": "web_search",
		},
	}
	tools = append(tools, assistantFunctionTools()...)
	return tools
}

func assistantFunctionTools() []map[string]interface{} {
	return []map[string]interface{}{
		{
			"type":        "function",
			"name":        assistantToolCreateActivity,
			"description": "Propose creating a new activity or itinerary item for this trip. Infer missing details (location, end time, etc.) from the trip context when the user leaves gaps, and clearly mention any assumptions you make.",
			"parameters": map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"name":        map[string]interface{}{"type": "string", "description": "Activity title"},
					"description": map[string]interface{}{"type": "string", "description": "Optional notes or details"},
					"address":     map[string]interface{}{"type": "string", "description": "Location or address (required in the form)"},
					"destination": map[string]interface{}{
						"type":        "object",
						"description": "Destination/place metadata (matches the Destination picker in the UI)",
						"properties": map[string]interface{}{
							"name":      map[string]interface{}{"type": "string"},
							"country":   map[string]interface{}{"type": "string"},
							"state":     map[string]interface{}{"type": "string"},
							"latitude":  map[string]interface{}{"type": "string"},
							"longitude": map[string]interface{}{"type": "string"},
							"timezone":  map[string]interface{}{"type": "string"},
							"category":  map[string]interface{}{"type": "string"},
							"place_id":  map[string]interface{}{"type": "string"},
						},
					},
					"start_time": map[string]interface{}{
						"type":        "string",
						"description": "Start time in RFC3339 format (local time of the location).",
					},
					"end_time": map[string]interface{}{
						"type":        "string",
						"description": "End time in RFC3339 format (local time).",
					},
					"notes":      map[string]interface{}{"type": "string", "description": "Internal notes/reminders"},
					"cost_value": map[string]interface{}{"type": "number", "description": "Estimated cost numeric value"},
					"cost_currency": map[string]interface{}{
						"type":        "string",
						"description": "Currency code for the cost (e.g., USD, EUR)",
					},
				},
				"required":             []string{"name", "address", "start_time"},
				"additionalProperties": false,
			},
		},
		{
			"type":        "function",
			"name":        assistantToolUpdateActivity,
			"description": "Update an existing activity. Always include the record_id shown in the trip context and provide only the fields that should change. Mention assumptions if you infer details.",
			"parameters": map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"record_id":   map[string]interface{}{"type": "string", "description": "Activity ID"},
					"name":        map[string]interface{}{"type": "string"},
					"description": map[string]interface{}{"type": "string"},
					"address":     map[string]interface{}{"type": "string"},
					"destination": map[string]interface{}{
						"type": "object",
						"properties": map[string]interface{}{
							"name":      map[string]interface{}{"type": "string"},
							"country":   map[string]interface{}{"type": "string"},
							"state":     map[string]interface{}{"type": "string"},
							"latitude":  map[string]interface{}{"type": "string"},
							"longitude": map[string]interface{}{"type": "string"},
							"timezone":  map[string]interface{}{"type": "string"},
							"category":  map[string]interface{}{"type": "string"},
							"place_id":  map[string]interface{}{"type": "string"},
						},
					},
					"start_time":    map[string]interface{}{"type": "string"},
					"end_time":      map[string]interface{}{"type": "string"},
					"notes":         map[string]interface{}{"type": "string"},
					"cost_value":    map[string]interface{}{"type": "number"},
					"cost_currency": map[string]interface{}{"type": "string"},
				},
				"required":             []string{"record_id"},
				"additionalProperties": false,
			},
		},
		{
			"type":        "function",
			"name":        assistantToolDeleteActivity,
			"description": "Delete an existing activity by record_id when the traveler asks to remove it.",
			"parameters": map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"record_id": map[string]interface{}{"type": "string"},
					"reason":    map[string]interface{}{"type": "string", "description": "Optional reason/reminder"},
				},
				"required":             []string{"record_id"},
				"additionalProperties": false,
			},
		},
		{
			"type":        "function",
			"name":        assistantToolCreateLodging,
			"description": "Propose adding a lodging or stay (hotel, rental, etc.) to this trip.",
			"parameters": map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"name":       map[string]interface{}{"type": "string", "description": "Property name"},
					"type":       map[string]interface{}{"type": "string", "description": "Lodging type (hotel, rental, etc.)"},
					"address":    map[string]interface{}{"type": "string", "description": "Address or area"},
					"start_time": map[string]interface{}{"type": "string", "description": "Check-in time/date in RFC3339"},
					"end_time":   map[string]interface{}{"type": "string", "description": "Check-out time/date in RFC3339"},
					"confirmation": map[string]interface{}{
						"type":        "string",
						"description": "Confirmation number or reservation code",
					},
					"notes": map[string]interface{}{"type": "string", "description": "Extra notes or reminders"},
				},
				"required":             []string{"name", "start_time", "end_time"},
				"additionalProperties": false,
			},
		},
		{
			"type":        "function",
			"name":        assistantToolUpdateLodging,
			"description": "Update an existing lodging entry. Always include record_id.",
			"parameters": map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"record_id":    map[string]interface{}{"type": "string"},
					"name":         map[string]interface{}{"type": "string"},
					"type":         map[string]interface{}{"type": "string"},
					"address":      map[string]interface{}{"type": "string"},
					"start_time":   map[string]interface{}{"type": "string"},
					"end_time":     map[string]interface{}{"type": "string"},
					"confirmation": map[string]interface{}{"type": "string"},
					"notes":        map[string]interface{}{"type": "string"},
				},
				"required":             []string{"record_id"},
				"additionalProperties": false,
			},
		},
		{
			"type":        "function",
			"name":        assistantToolDeleteLodging,
			"description": "Delete an existing lodging entry.",
			"parameters": map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"record_id": map[string]interface{}{"type": "string"},
					"reason":    map[string]interface{}{"type": "string"},
				},
				"required":             []string{"record_id"},
				"additionalProperties": false,
			},
		},
		{
			"type":        "function",
			"name":        assistantToolCreateTransportation,
			"description": "Propose a transportation segment (flight, train, transfer, etc.). Infer missing destination or arrival details from the context when the traveler is vague, and mention assumptions.",
			"parameters": map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"type":        map[string]interface{}{"type": "string", "description": "Transportation type, e.g., flight, train"},
					"provider":    map[string]interface{}{"type": "string", "description": "Carrier or provider"},
					"origin":      map[string]interface{}{"type": "string", "description": "Origin city or location"},
					"destination": map[string]interface{}{"type": "string", "description": "Destination city or location"},
					"departure_time": map[string]interface{}{
						"type":        "string",
						"description": "Departure time in RFC3339",
					},
					"arrival_time": map[string]interface{}{
						"type":        "string",
						"description": "Arrival time in RFC3339",
					},
					"notes": map[string]interface{}{"type": "string", "description": "Extra notes (confirmation, seats, etc.)"},
				},
				"required":             []string{"type", "origin", "departure_time"},
				"additionalProperties": false,
			},
		},
		{
			"type":        "function",
			"name":        assistantToolUpdateTransportation,
			"description": "Update an existing transportation entry. Include the record_id and any fields that need to change.",
			"parameters": map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"record_id":      map[string]interface{}{"type": "string"},
					"type":           map[string]interface{}{"type": "string"},
					"provider":       map[string]interface{}{"type": "string"},
					"origin":         map[string]interface{}{"type": "string"},
					"destination":    map[string]interface{}{"type": "string"},
					"departure_time": map[string]interface{}{"type": "string"},
					"arrival_time":   map[string]interface{}{"type": "string"},
					"notes":          map[string]interface{}{"type": "string"},
				},
				"required":             []string{"record_id"},
				"additionalProperties": false,
			},
		},
		{
			"type":        "function",
			"name":        assistantToolDeleteTransportation,
			"description": "Delete a transportation entry by record_id.",
			"parameters": map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"record_id": map[string]interface{}{"type": "string"},
					"reason":    map[string]interface{}{"type": "string"},
				},
				"required":             []string{"record_id"},
				"additionalProperties": false,
			},
		},
	}
}

func storeAssistantProposal(proposal *assistantProposal) {
	proposalStore.Lock()
	defer proposalStore.Unlock()
	proposalStore.items[proposal.ID] = proposal
}

func popAssistantProposal(id string) (*assistantProposal, bool) {
	proposalStore.Lock()
	defer proposalStore.Unlock()
	proposal, ok := proposalStore.items[id]
	if ok {
		delete(proposalStore.items, id)
	}
	return proposal, ok
}

func getAssistantProposal(id string) (*assistantProposal, bool) {
	proposalStore.RLock()
	defer proposalStore.RUnlock()
	proposal, ok := proposalStore.items[id]
	return proposal, ok
}

func summarizeProposal(tool string, args map[string]interface{}) string {
	switch tool {
	case assistantToolCreateActivity:
		return fmt.Sprintf("I'll add an activity \"%s\" starting %s.", stringValue(args["name"]), stringValue(args["start_time"]))
	case assistantToolUpdateActivity:
		return fmt.Sprintf("I'll update activity %s.", stringValue(args["record_id"]))
	case assistantToolDeleteActivity:
		return fmt.Sprintf("I'll delete activity %s.", stringValue(args["record_id"]))
	case assistantToolCreateLodging:
		return fmt.Sprintf("I'll add lodging \"%s\" from %s to %s.", stringValue(args["name"]), stringValue(args["start_time"]), stringValue(args["end_time"]))
	case assistantToolUpdateLodging:
		return fmt.Sprintf("I'll update lodging %s.", stringValue(args["record_id"]))
	case assistantToolDeleteLodging:
		return fmt.Sprintf("I'll delete lodging %s.", stringValue(args["record_id"]))
	case assistantToolCreateTransportation:
		return fmt.Sprintf("I'll add %s from %s to %s departing %s.", stringValue(args["type"]), stringValue(args["origin"]), stringValue(args["destination"]), stringValue(args["departure_time"]))
	case assistantToolUpdateTransportation:
		return fmt.Sprintf("I'll update transportation %s.", stringValue(args["record_id"]))
	case assistantToolDeleteTransportation:
		return fmt.Sprintf("I'll delete transportation %s.", stringValue(args["record_id"]))
	default:
		return "I have a change ready to apply."
	}
}

func (p *assistantProposal) expired() bool {
	return time.Now().UTC().After(p.ExpiresAt)
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

type functionCallBuffer struct {
	active   bool
	name     string
	itemID   string
	builder  strings.Builder
	proposal *assistantProposal
}

func (b *functionCallBuffer) handleOutputItemAdded(item map[string]interface{}) {
	itemType := stringValue(item["type"])
	if itemType != "function_call" {
		return
	}
	b.active = true
	b.name = stringValue(item["name"])
	b.itemID = stringValue(item["id"])
	b.builder.Reset()
}

func (b *functionCallBuffer) handleArgumentsDelta(event map[string]interface{}) {
	if !b.active {
		return
	}
	itemID := stringValue(event["item_id"])
	if itemID != "" && itemID != b.itemID {
		return
	}
	delta, _ := event["delta"].(string)
	if delta != "" {
		b.builder.WriteString(delta)
	}
}

func (b *functionCallBuffer) finalizeProposal(event map[string]interface{}, tripID string) (map[string]interface{}, bool) {
	if !b.active {
		return nil, false
	}
	itemID := stringValue(event["item_id"])
	if itemID != "" && itemID != b.itemID {
		return nil, false
	}

	argsJSON := strings.TrimSpace(b.builder.String())
	if argsJSON == "" {
		return nil, false
	}

	var args map[string]interface{}
	if err := json.Unmarshal([]byte(argsJSON), &args); err != nil {
		return nil, false
	}

	proposal := &assistantProposal{
		ID:        uuid.NewString(),
		TripID:    tripID,
		Tool:      b.name,
		Arguments: args,
		CreatedAt: time.Now().UTC(),
		ExpiresAt: time.Now().UTC().Add(proposalTTL),
	}
	storeAssistantProposal(proposal)
	summary := summarizeProposal(proposal.Tool, proposal.Arguments)
	b.active = false
	b.builder.Reset()
	b.itemID = ""

	return map[string]interface{}{
		"type": "proposal",
		"proposal": map[string]interface{}{
			"id":        proposal.ID,
			"tool":      proposal.Tool,
			"arguments": proposal.Arguments,
			"summary":   summary,
			"expiresAt": proposal.ExpiresAt.Format(time.RFC3339),
		},
	}, true
}
