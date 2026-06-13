package routes

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
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

type tripAssistantStoredMessage struct {
	Id       string                 `json:"id"`
	Role     string                 `json:"role"`
	Content  string                 `json:"content"`
	Metadata map[string]interface{} `json:"metadata,omitempty"`
	Created  string                 `json:"created"`
}

type tripAssistantMessageRequest struct {
	Role     string                 `json:"role"`
	Content  string                 `json:"content"`
	Metadata map[string]interface{} `json:"metadata,omitempty"`
}

type assistantSource struct {
	Title string `json:"title,omitempty"`
	URL   string `json:"url"`
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

const proposalTTL = 24 * time.Hour

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
	Id        string                 `json:"id"`
	TripID    string                 `json:"trip"`
	UserID    string                 `json:"user"`
	Status    string                 `json:"status"`
	Action    string                 `json:"actionType"`
	Changes   []assistantChange      `json:"changes"`
	Summary   string                 `json:"summary"`
	Preview   map[string]interface{} `json:"preview"`
	Sources   []assistantSource      `json:"sources,omitempty"`
	ExpiresAt string                 `json:"expiresAt"`
	Created   string                 `json:"created"`
	Updated   string                 `json:"updated"`
	Error     string                 `json:"error,omitempty"`
	Result    map[string]interface{} `json:"result,omitempty"`
}

type assistantChange struct {
	Operation   string                 `json:"operation"`
	EntityType  string                 `json:"entity_type"`
	RecordID    *string                `json:"record_id"`
	Fields      map[string]interface{} `json:"fields"`
	Clear       []string               `json:"clear"`
	Reason      *string                `json:"reason"`
	Confidence  float64                `json:"confidence"`
	Assumptions []string               `json:"assumptions"`
	Warnings    []string               `json:"warnings"`
}

type assistantProposalArguments struct {
	Title       string            `json:"title"`
	Summary     string            `json:"summary"`
	Changes     []assistantChange `json:"changes"`
	Assumptions []string          `json:"assumptions"`
	Warnings    []string          `json:"warnings"`
}

const (
	openAIModel             = "gpt-5.4-mini"
	tripAssistantModel      = openAIModel
	tripAssistantMaxHistory = 30
)

func TripAssistant(e *core.RequestEvent) error {
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

	tripRecord, err := tripRecordFromRequest(e)
	if err != nil {
		return e.JSON(http.StatusBadRequest, map[string]string{"error": err.Error()})
	}

	info, err := e.RequestInfo()
	if err != nil {
		return err
	}
	if err := requireTripAssistantUpdateAccess(e, tripRecord, info); err != nil {
		return err
	}

	userContent := strings.TrimSpace(req.Messages[len(req.Messages)-1].Content)
	if userContent == "" {
		return e.JSON(http.StatusBadRequest, map[string]string{"error": "content is required"})
	}

	if _, err := createTripAssistantMessage(e.App, tripRecord.Id, info.Auth.Id, "user", userContent, nil); err != nil {
		return e.JSON(http.StatusInternalServerError, map[string]string{"error": "unable to save assistant message"})
	}

	reply, metadata, err := runAssistantOnce(e.Request.Context(), e.App, tripRecord, info.Auth.Id)
	if err != nil {
		e.App.Logger().Error("TripAssistant agent failed", "error", err, "tripId", tripRecord.Id)
		return e.JSON(http.StatusBadGateway, map[string]string{"error": err.Error()})
	}

	if _, err := createTripAssistantMessage(e.App, tripRecord.Id, info.Auth.Id, "assistant", reply, metadata); err != nil {
		e.App.Logger().Warn("TripAssistant failed to persist reply", "error", err, "tripId", tripRecord.Id)
	}

	return e.JSON(http.StatusOK, tripAssistantResponse{
		Message: assistantMessage{
			Role:    "assistant",
			Content: reply,
		},
	})
}

func requireAssistantConfigured(e *core.RequestEvent) error {
	if strings.TrimSpace(os.Getenv("OPENAI_API_KEY")) == "" {
		return e.JSON(http.StatusServiceUnavailable, map[string]string{
			"error": "OPENAI_API_KEY is not configured on the server",
		})
	}
	if _, err := resolveAgentRunnerPath(); err != nil {
		return e.JSON(http.StatusServiceUnavailable, map[string]string{
			"error": err.Error(),
		})
	}
	return nil
}

func ListTripAssistantMessages(e *core.RequestEvent) error {
	tripRecord, err := tripRecordFromRequest(e)
	if err != nil {
		return e.JSON(http.StatusBadRequest, map[string]string{"error": err.Error()})
	}

	info, err := e.RequestInfo()
	if err != nil {
		return err
	}
	if err := requireTripAssistantUpdateAccess(e, tripRecord, info); err != nil {
		return err
	}

	records, err := e.App.FindAllRecords(
		"trip_assistant_messages",
		dbx.NewExp("trip = {:tripId} and user = {:userId}", dbx.Params{"tripId": tripRecord.Id, "userId": info.Auth.Id}),
	)
	if err != nil {
		return e.JSON(http.StatusInternalServerError, map[string]string{"error": "unable to load assistant messages"})
	}

	sort.Slice(records, func(i, j int) bool {
		return records[i].GetDateTime("created").Time().Before(records[j].GetDateTime("created").Time())
	})

	messages := make([]tripAssistantStoredMessage, 0, len(records))
	for _, record := range records {
		messages = append(messages, storedMessageFromRecord(record))
	}

	return e.JSON(http.StatusOK, map[string]interface{}{"messages": messages})
}

func CreateTripAssistantMessage(e *core.RequestEvent) error {
	tripRecord, err := tripRecordFromRequest(e)
	if err != nil {
		return e.JSON(http.StatusBadRequest, map[string]string{"error": err.Error()})
	}

	info, err := e.RequestInfo()
	if err != nil {
		return err
	}
	if err := requireTripAssistantUpdateAccess(e, tripRecord, info); err != nil {
		return err
	}

	var req tripAssistantMessageRequest
	if err := json.NewDecoder(e.Request.Body).Decode(&req); err != nil {
		return e.JSON(http.StatusBadRequest, map[string]string{"error": "invalid request body"})
	}

	if req.Role != "user" {
		return e.JSON(http.StatusBadRequest, map[string]string{"error": "only user messages can be created from the client"})
	}

	if strings.TrimSpace(req.Content) == "" {
		return e.JSON(http.StatusBadRequest, map[string]string{"error": "content is required"})
	}

	record, err := createTripAssistantMessage(e.App, tripRecord.Id, info.Auth.Id, req.Role, req.Content, nil)
	if err != nil {
		return e.JSON(http.StatusInternalServerError, map[string]string{"error": "unable to save assistant message"})
	}

	return e.JSON(http.StatusOK, map[string]interface{}{"message": storedMessageFromRecord(record)})
}

func ClearTripAssistantMessages(e *core.RequestEvent) error {
	tripRecord, err := tripRecordFromRequest(e)
	if err != nil {
		return e.JSON(http.StatusBadRequest, map[string]string{"error": err.Error()})
	}

	info, err := e.RequestInfo()
	if err != nil {
		return err
	}
	if err := requireTripAssistantUpdateAccess(e, tripRecord, info); err != nil {
		return err
	}

	records, err := e.App.FindAllRecords(
		"trip_assistant_messages",
		dbx.NewExp("trip = {:tripId} and user = {:userId}", dbx.Params{"tripId": tripRecord.Id, "userId": info.Auth.Id}),
	)
	if err != nil {
		return e.JSON(http.StatusInternalServerError, map[string]string{"error": "unable to load assistant messages"})
	}

	for _, record := range records {
		if err := e.App.Delete(record); err != nil {
			return e.JSON(http.StatusInternalServerError, map[string]string{"error": "unable to clear assistant messages"})
		}
	}

	if err := expirePendingAssistantProposals(e.App, tripRecord.Id, info.Auth.Id); err != nil {
		e.App.Logger().Warn("TripAssistant failed to expire proposals during clear", "error", err, "tripId", tripRecord.Id)
	}

	return e.JSON(http.StatusOK, map[string]interface{}{"deleted": len(records)})
}

func tripRecordFromRequest(e *core.RequestEvent) (*core.Record, error) {
	tripVal := e.Get("trip")
	if tripVal == nil {
		return nil, errors.New("trip context is missing")
	}

	tripRecord, ok := tripVal.(*core.Record)
	if !ok {
		return nil, errors.New("unable to read trip info")
	}

	return tripRecord, nil
}

func createTripAssistantMessage(app core.App, tripID, userID, role, content string, metadata map[string]interface{}) (*core.Record, error) {
	collection, err := app.FindCollectionByNameOrId("trip_assistant_messages")
	if err != nil {
		return nil, err
	}

	record := core.NewRecord(collection)
	record.Set("trip", tripID)
	record.Set("user", userID)
	record.Set("role", role)
	record.Set("content", strings.TrimSpace(content))
	if len(metadata) > 0 {
		record.Set("metadata", metadata)
	}

	if err := app.Save(record); err != nil {
		return nil, err
	}

	return record, nil
}

func saveTripAssistantMessage(app core.App, tripID, userID, role, content string, metadata map[string]interface{}) error {
	if strings.TrimSpace(content) == "" {
		return nil
	}
	_, err := createTripAssistantMessage(app, tripID, userID, role, content, metadata)
	return err
}

func storedMessageFromRecord(record *core.Record) tripAssistantStoredMessage {
	metadata := mapValue(record.Get("metadata"))
	if len(metadata) == 0 {
		metadata = nil
	}

	return tripAssistantStoredMessage{
		Id:       record.Id,
		Role:     record.GetString("role"),
		Content:  record.GetString("content"),
		Metadata: metadata,
		Created:  record.GetDateTime("created").Time().Format(time.RFC3339),
	}
}

func requireTripAssistantUpdateAccess(e *core.RequestEvent, tripRecord *core.Record, info *core.RequestInfo) error {
	canUpdate, err := e.App.CanAccessRecord(tripRecord, info, tripRecord.Collection().UpdateRule)
	if err != nil {
		return err
	}
	if !canUpdate {
		return e.ForbiddenError("Assistant access requires edit access to this trip", nil)
	}
	return nil
}

func TripAssistantStream(e *core.RequestEvent) error {
	if err := requireAssistantConfigured(e); err != nil {
		return err
	}

	info, err := e.RequestInfo()
	if err != nil {
		return err
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
	if err := requireTripAssistantUpdateAccess(e, tripRecord, info); err != nil {
		return err
	}

	userContent := strings.TrimSpace(req.Messages[len(req.Messages)-1].Content)
	if userContent == "" {
		return e.JSON(http.StatusBadRequest, map[string]string{"error": "content is required"})
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

	userRecord, err := createTripAssistantMessage(e.App, tripRecord.Id, info.Auth.Id, "user", userContent, nil)
	if err != nil {
		sendSSEEvent(writer, flusher, map[string]string{
			"type":    "error",
			"message": "unable to save assistant message",
		})
		return nil
	}
	sendSSEEvent(writer, flusher, map[string]interface{}{
		"type":    "message_created",
		"message": storedMessageFromRecord(userRecord),
	})

	if err := streamAgentToClient(e.Request.Context(), e.App, writer, flusher, tripRecord, info.Auth.Id); err != nil {
		e.App.Logger().Error("TripAssistant stream failed", "error", err, "tripId", tripRecord.Id)
		sendSSEEvent(writer, flusher, map[string]string{
			"type":    "error",
			"message": assistantClientError(err),
		})
	}

	return nil
}

type proposalDecisionRequest struct {
	Decision string `json:"decision"`
}

func ListAssistantProposals(e *core.RequestEvent) error {
	tripRecord, err := tripRecordFromRequest(e)
	if err != nil {
		return e.JSON(http.StatusBadRequest, map[string]string{"error": err.Error()})
	}

	info, err := e.RequestInfo()
	if err != nil {
		return err
	}
	if err := requireTripAssistantUpdateAccess(e, tripRecord, info); err != nil {
		return err
	}

	_ = markExpiredAssistantProposals(e.App, tripRecord.Id, info.Auth.Id)
	records, err := e.App.FindAllRecords(
		"trip_assistant_proposals",
		dbx.NewExp("trip = {:tripId} and user = {:userId}", dbx.Params{"tripId": tripRecord.Id, "userId": info.Auth.Id}),
	)
	if err != nil {
		return e.JSON(http.StatusInternalServerError, map[string]string{"error": "unable to load assistant proposals"})
	}

	sort.Slice(records, func(i, j int) bool {
		return records[i].GetDateTime("created").Time().Before(records[j].GetDateTime("created").Time())
	})

	proposals := make([]assistantProposal, 0, len(records))
	for _, record := range records {
		proposals = append(proposals, proposalFromRecord(record))
	}
	return e.JSON(http.StatusOK, map[string]interface{}{"proposals": proposals})
}

func AssistantProposalDecision(e *core.RequestEvent) error {
	tripVal := e.Get("trip")
	if tripVal == nil {
		return e.JSON(http.StatusBadRequest, map[string]string{"error": "trip context missing"})
	}
	tripRecord := tripVal.(*core.Record)

	info, err := e.RequestInfo()
	if err != nil {
		return err
	}
	if err := requireTripAssistantUpdateAccess(e, tripRecord, info); err != nil {
		return err
	}

	proposalID := e.Request.PathValue("proposalId")
	if proposalID == "" {
		return e.JSON(http.StatusBadRequest, map[string]string{"error": "proposal id missing"})
	}

	var req proposalDecisionRequest
	if err := json.NewDecoder(e.Request.Body).Decode(&req); err != nil {
		return e.JSON(http.StatusBadRequest, map[string]string{"error": "invalid body"})
	}

	proposal, err := e.App.FindRecordById("trip_assistant_proposals", proposalID)
	if err != nil {
		return e.JSON(http.StatusNotFound, map[string]string{"error": "proposal not found"})
	}
	if err := requireProposalOwnership(proposal, tripRecord.Id, info.Auth.Id); err != nil {
		return e.JSON(http.StatusForbidden, map[string]string{"error": err.Error()})
	}

	switch strings.ToLower(req.Decision) {
	case "approve":
		if proposal.GetString("status") != "pending" && proposal.GetString("status") != "failed" {
			return e.JSON(http.StatusConflict, map[string]string{"error": "proposal is not pending"})
		}
		if proposalExpired(proposal) {
			proposal.Set("status", "expired")
			_ = e.App.Save(proposal)
			return e.JSON(http.StatusGone, map[string]string{"error": "proposal expired"})
		}
		message, err := approveAssistantProposal(e.Request.Context(), e.App, tripRecord, info.Auth.Id, proposal)
		if err != nil {
			return e.JSON(http.StatusInternalServerError, map[string]string{"error": err.Error()})
		}
		return e.JSON(http.StatusOK, map[string]interface{}{
			"status":   "approved",
			"message":  message,
			"proposal": proposalFromRecord(proposal),
		})
	case "decline", "reject":
		message := "Okay, I will skip that change."
		proposal.Set("status", "rejected")
		proposal.Set("error", "")
		if err := e.App.Save(proposal); err != nil {
			return e.JSON(http.StatusInternalServerError, map[string]string{"error": "unable to reject proposal"})
		}
		if reply, err := resumeAssistantProposal(e.Request.Context(), e.App, tripRecord.Id, info.Auth.Id, proposal, "reject"); err == nil && strings.TrimSpace(reply) != "" {
			message = reply
		}
		if err := saveTripAssistantMessage(e.App, tripRecord.Id, info.Auth.Id, "assistant", message, map[string]interface{}{"proposalId": proposal.Id}); err != nil {
			e.App.Logger().Warn("TripAssistant failed to persist proposal decline", "error", err, "tripId", tripRecord.Id)
		}
		return e.JSON(http.StatusOK, map[string]interface{}{
			"status":   "rejected",
			"message":  message,
			"proposal": proposalFromRecord(proposal),
		})
	case "timeout":
		message := "The request expired. Ask again if you'd like me to re-create it."
		proposal.Set("status", "expired")
		_ = e.App.Save(proposal)
		if err := saveTripAssistantMessage(e.App, tripRecord.Id, info.Auth.Id, "assistant", message, map[string]interface{}{"proposalId": proposal.Id}); err != nil {
			e.App.Logger().Warn("TripAssistant failed to persist proposal timeout", "error", err, "tripId", tripRecord.Id)
		}
		return e.JSON(http.StatusOK, map[string]interface{}{
			"status":   "timeout",
			"message":  message,
			"proposal": proposalFromRecord(proposal),
		})
	default:
		return e.JSON(http.StatusBadRequest, map[string]string{"error": "decision must be approve, reject, decline, or timeout"})
	}
}

func RetryAssistantProposal(e *core.RequestEvent) error {
	tripRecord, err := tripRecordFromRequest(e)
	if err != nil {
		return e.JSON(http.StatusBadRequest, map[string]string{"error": err.Error()})
	}
	info, err := e.RequestInfo()
	if err != nil {
		return err
	}
	if err := requireTripAssistantUpdateAccess(e, tripRecord, info); err != nil {
		return err
	}

	proposalID := e.Request.PathValue("proposalId")
	proposal, err := e.App.FindRecordById("trip_assistant_proposals", proposalID)
	if err != nil {
		return e.JSON(http.StatusNotFound, map[string]string{"error": "proposal not found"})
	}
	if err := requireProposalOwnership(proposal, tripRecord.Id, info.Auth.Id); err != nil {
		return e.JSON(http.StatusForbidden, map[string]string{"error": err.Error()})
	}
	if proposal.GetString("status") != "failed" {
		return e.JSON(http.StatusConflict, map[string]string{"error": "only failed proposals can be retried"})
	}
	message, err := approveAssistantProposal(e.Request.Context(), e.App, tripRecord, info.Auth.Id, proposal)
	if err != nil {
		return e.JSON(http.StatusInternalServerError, map[string]string{"error": err.Error()})
	}
	return e.JSON(http.StatusOK, map[string]interface{}{
		"status":   "approved",
		"message":  message,
		"proposal": proposalFromRecord(proposal),
	})
}

func approveAssistantProposal(ctx context.Context, app core.App, trip *core.Record, userID string, proposal *core.Record) (string, error) {
	proposal.Set("status", "applying")
	proposal.Set("error", "")
	if err := app.Save(proposal); err != nil {
		return "", err
	}

	result, err := applyAssistantProposalBatch(app, trip.Id, proposal)
	if err != nil {
		proposal.Set("status", "failed")
		proposal.Set("error", err.Error())
		proposal.Set("result", map[string]interface{}{"applied": false})
		_ = app.Save(proposal)
		return "", err
	}

	proposal.Set("status", "approved")
	proposal.Set("result", result)
	proposal.Set("error", "")
	if err := app.Save(proposal); err != nil {
		return "", err
	}

	message, err := resumeAssistantProposal(ctx, app, trip.Id, userID, proposal, "approve")
	if err != nil || strings.TrimSpace(message) == "" {
		message = result["message"].(string)
	}
	if err := saveTripAssistantMessage(app, trip.Id, userID, "assistant", message, map[string]interface{}{"proposalId": proposal.Id}); err != nil {
		app.Logger().Warn("TripAssistant failed to persist proposal approval", "error", err, "tripId", trip.Id)
	}
	return message, nil
}

func applyAssistantProposalBatch(app core.App, tripID string, proposal *core.Record) (map[string]interface{}, error) {
	var changes []assistantChange
	if err := proposal.UnmarshalJSONField("changes", &changes); err != nil {
		return nil, err
	}
	if len(changes) == 0 {
		return nil, errors.New("proposal has no changes")
	}

	applied := make([]map[string]interface{}, 0, len(changes))
	for index, change := range changes {
		record, err := applyAssistantChange(app, tripID, change)
		if err != nil {
			return nil, fmt.Errorf("change %d failed: %w", index+1, err)
		}
		applied = append(applied, record)
	}

	message := fmt.Sprintf("Applied %d itinerary change.", len(applied))
	if len(applied) != 1 {
		message = fmt.Sprintf("Applied %d itinerary changes.", len(applied))
	}
	return map[string]interface{}{
		"applied": true,
		"changes": applied,
		"message": message,
	}, nil
}

func applyAssistantChange(app core.App, tripID string, change assistantChange) (map[string]interface{}, error) {
	collectionName, err := collectionForEntity(change.EntityType)
	if err != nil {
		return nil, err
	}
	switch change.Operation {
	case "create":
		collection, err := app.FindCollectionByNameOrId(collectionName)
		if err != nil {
			return nil, err
		}
		record := core.NewRecord(collection)
		record.Set("trip", tripID)
		if err := applyChangeFields(record, change, true); err != nil {
			return nil, err
		}
		if err := app.Save(record); err != nil {
			return nil, err
		}
		return map[string]interface{}{"operation": "create", "entity_type": change.EntityType, "record_id": record.Id}, nil
	case "update":
		recordID := deref(change.RecordID)
		record, err := ensureTripRecord(app, collectionName, recordID, tripID)
		if err != nil {
			return nil, err
		}
		if err := applyChangeFields(record, change, false); err != nil {
			return nil, err
		}
		if err := app.Save(record); err != nil {
			return nil, err
		}
		return map[string]interface{}{"operation": "update", "entity_type": change.EntityType, "record_id": record.Id}, nil
	case "delete":
		recordID := deref(change.RecordID)
		record, err := ensureTripRecord(app, collectionName, recordID, tripID)
		if err != nil {
			return nil, err
		}
		if err := app.Delete(record); err != nil {
			return nil, err
		}
		return map[string]interface{}{"operation": "delete", "entity_type": change.EntityType, "record_id": recordID}, nil
	default:
		return nil, errors.New("unsupported operation")
	}
}

func applyChangeFields(record *core.Record, change assistantChange, creating bool) error {
	for _, field := range change.Clear {
		if pbField := proposalFieldToPocketBase(change.EntityType, field); pbField != "" {
			record.Set(pbField, nil)
		}
	}
	for field, value := range change.Fields {
		if value == nil {
			continue
		}
		if pbField := proposalFieldToPocketBase(change.EntityType, field); pbField != "" {
			record.Set(pbField, value)
		}
	}

	if costValue, ok := change.Fields["cost_value"]; ok && costValue != nil {
		currency := stringValue(change.Fields["cost_currency"])
		if currency == "" {
			currency = "USD"
		}
		record.Set("cost", map[string]interface{}{"value": floatValue(costValue), "currency": currency})
	}
	if metadata := mapValue(change.Fields["metadata"]); len(metadata) > 0 {
		record.Set("metadata", metadata)
	}

	return validateRecordForChange(record, change, creating)
}

func validateRecordForChange(record *core.Record, change assistantChange, creating bool) error {
	required := map[string][]string{
		"activity":       {"name", "startDate"},
		"lodging":        {"name", "type", "address", "startDate", "endDate"},
		"transportation": {"type", "origin", "destination", "departureTime", "arrivalTime"},
	}
	if creating {
		for _, field := range required[change.EntityType] {
			if strings.TrimSpace(record.GetString(field)) == "" {
				return fmt.Errorf("%s is required", field)
			}
		}
	}

	startField, endField := timeFieldsForEntity(change.EntityType)
	if startField != "" && endField != "" {
		start := record.GetDateTime(startField).Time()
		end := record.GetDateTime(endField).Time()
		if !start.IsZero() && !end.IsZero() && !end.After(start) {
			return errors.New("end time must be after start time")
		}
	}
	return nil
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

func compactAssistantHistory(messages []assistantMessage) []assistantMessage {
	if len(messages) <= tripAssistantMaxHistory {
		return messages
	}
	return messages[len(messages)-tripAssistantMaxHistory:]
}

type agentRunnerInput struct {
	Mode             string             `json:"mode"`
	Model            string             `json:"model"`
	Messages         []assistantMessage `json:"messages,omitempty"`
	TripContext      interface{}        `json:"tripContext,omitempty"`
	SDKState         string             `json:"sdkState,omitempty"`
	Decision         string             `json:"decision,omitempty"`
	RejectionMessage string             `json:"rejectionMessage,omitempty"`
}

type agentRunnerEvent struct {
	Type          string                 `json:"type"`
	Text          string                 `json:"text,omitempty"`
	Message       string                 `json:"message,omitempty"`
	FinalOutput   interface{}            `json:"finalOutput,omitempty"`
	Arguments     interface{}            `json:"arguments,omitempty"`
	SDKState      string                 `json:"sdkState,omitempty"`
	Interruptions interface{}            `json:"interruptions,omitempty"`
	Sources       []assistantSource      `json:"sources,omitempty"`
	Raw           map[string]interface{} `json:"-"`
}

func runAssistantOnce(ctx context.Context, app core.App, trip *core.Record, userID string) (string, map[string]interface{}, error) {
	events, err := invokeAgentRunner(ctx, app, trip, userID, agentRunnerInput{Mode: "run", Model: tripAssistantModel})
	if err != nil {
		return "", nil, err
	}
	var text strings.Builder
	sources := make([]assistantSource, 0)
	for _, event := range events {
		switch event.Type {
		case "text_delta":
			text.WriteString(event.Text)
		case "sources":
			sources = append(sources, event.Sources...)
		case "proposal_interruption":
			return "", nil, errors.New("proposal approval is only supported over the stream endpoint")
		case "done":
			if strings.TrimSpace(text.String()) == "" {
				text.WriteString(finalOutputText(event.FinalOutput))
			}
		case "error":
			return "", nil, errors.New(event.Message)
		}
	}
	metadata := map[string]interface{}{}
	if len(sources) > 0 {
		metadata["sources"] = sources
	}
	return strings.TrimSpace(text.String()), metadata, nil
}

func streamAgentToClient(ctx context.Context, app core.App, writer http.ResponseWriter, flusher http.Flusher, trip *core.Record, userID string) error {
	events, err := invokeAgentRunner(ctx, app, trip, userID, agentRunnerInput{Mode: "run", Model: tripAssistantModel})
	if err != nil {
		return err
	}

	var assistantText strings.Builder
	sources := make([]assistantSource, 0)
	persisted := false

	for _, event := range events {
		switch event.Type {
		case "text_delta":
			assistantText.WriteString(event.Text)
			sendSSEEvent(writer, flusher, map[string]string{"type": "text_delta", "text": event.Text})
		case "sources":
			sources = append(sources, event.Sources...)
			sendSSEEvent(writer, flusher, map[string]interface{}{"type": "sources", "sources": event.Sources})
		case "proposal_interruption":
			if strings.TrimSpace(assistantText.String()) != "" {
				metadata := map[string]interface{}{}
				if len(sources) > 0 {
					metadata["sources"] = sources
				}
				_ = saveTripAssistantMessage(app, trip.Id, userID, "assistant", assistantText.String(), metadata)
			}
			proposal, err := createAssistantProposalRecord(app, trip, userID, event)
			if err != nil {
				return err
			}
			sendSSEEvent(writer, flusher, map[string]interface{}{"type": "proposal_created", "proposal": proposalFromRecord(proposal)})
			sendSSEEvent(writer, flusher, map[string]string{"type": "done"})
			persisted = true
		case "done":
			if !persisted {
				if strings.TrimSpace(assistantText.String()) == "" {
					finalText := finalOutputText(event.FinalOutput)
					if strings.TrimSpace(finalText) != "" {
						assistantText.WriteString(finalText)
						sendSSEEvent(writer, flusher, map[string]string{"type": "text_delta", "text": finalText})
					}
				}
				metadata := map[string]interface{}{}
				if len(sources) > 0 {
					metadata["sources"] = sources
				}
				if strings.TrimSpace(assistantText.String()) != "" {
					if _, err := createTripAssistantMessage(app, trip.Id, userID, "assistant", assistantText.String(), metadata); err != nil {
						app.Logger().Warn("TripAssistant failed to persist stream reply", "error", err, "tripId", trip.Id)
					}
				}
				sendSSEEvent(writer, flusher, map[string]string{"type": "done"})
				persisted = true
			}
		case "error":
			return errors.New(event.Message)
		}
	}

	if !persisted && strings.TrimSpace(assistantText.String()) != "" {
		metadata := map[string]interface{}{}
		if len(sources) > 0 {
			metadata["sources"] = sources
		}
		_ = saveTripAssistantMessage(app, trip.Id, userID, "assistant", assistantText.String(), metadata)
		sendSSEEvent(writer, flusher, map[string]string{"type": "done"})
	}
	return nil
}

func finalOutputText(value interface{}) string {
	switch typed := value.(type) {
	case string:
		return typed
	case []interface{}:
		var text strings.Builder
		for _, entry := range typed {
			text.WriteString(finalOutputText(entry))
		}
		return text.String()
	case map[string]interface{}:
		if content, ok := typed["content"]; ok {
			return finalOutputText(content)
		}
		if text, ok := typed["text"]; ok {
			return finalOutputText(text)
		}
		if output, ok := typed["output_text"]; ok {
			return finalOutputText(output)
		}
	}
	return ""
}

func assistantClientError(err error) string {
	message := strings.TrimSpace(err.Error())
	if message == "" {
		return "assistant request failed"
	}
	message = strings.ReplaceAll(message, "\r", " ")
	message = strings.ReplaceAll(message, "\n", " ")
	if len(message) > 500 {
		return message[:500] + "..."
	}
	return message
}

func invokeAgentRunner(ctx context.Context, app core.App, trip *core.Record, userID string, input agentRunnerInput) ([]agentRunnerEvent, error) {
	runnerPath, err := resolveAgentRunnerPath()
	if err != nil {
		return nil, err
	}
	if strings.TrimSpace(os.Getenv("OPENAI_API_KEY")) == "" {
		return nil, errors.New("OPENAI_API_KEY is not configured on the server")
	}

	if input.Mode == "run" {
		if trip == nil {
			return nil, errors.New("trip is required for assistant runs")
		}
		tripContext, err := buildTripAssistantContext(app, trip)
		if err != nil {
			return nil, err
		}
		messages, err := loadAssistantHistory(app, trip.Id, userID)
		if err != nil {
			return nil, err
		}
		input.TripContext = tripContext
		input.Messages = compactAssistantHistory(messages)
	}
	input.Model = tripAssistantModel

	body, err := json.Marshal(input)
	if err != nil {
		return nil, err
	}

	cmd := exec.CommandContext(ctx, "node", runnerPath)
	cmd.Stdin = strings.NewReader(string(body))
	cmd.Env = os.Environ()
	stderr, err := cmd.StderrPipe()
	if err != nil {
		return nil, err
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, err
	}
	if err := cmd.Start(); err != nil {
		return nil, err
	}

	events := make([]agentRunnerEvent, 0)
	scanner := bufio.NewScanner(stdout)
	scanner.Buffer(make([]byte, 0, 64*1024), 8*1024*1024)
	for scanner.Scan() {
		var raw map[string]interface{}
		if err := json.Unmarshal(scanner.Bytes(), &raw); err != nil {
			continue
		}
		event := agentRunnerEvent{Raw: raw, Type: stringValue(raw["type"]), Text: stringValue(raw["text"]), Message: stringValue(raw["message"]), SDKState: stringValue(raw["sdkState"])}
		if raw["arguments"] != nil {
			event.Arguments = raw["arguments"]
		}
		if raw["interruptions"] != nil {
			event.Interruptions = raw["interruptions"]
		}
		if raw["finalOutput"] != nil {
			event.FinalOutput = raw["finalOutput"]
		}
		if srcRaw, ok := raw["sources"].([]interface{}); ok {
			event.Sources = decodeAssistantSources(srcRaw)
		}
		events = append(events, event)
	}
	stderrBytes, _ := io.ReadAll(stderr)
	waitErr := cmd.Wait()
	if scanErr := scanner.Err(); scanErr != nil {
		return events, scanErr
	}
	if waitErr != nil {
		for _, event := range events {
			if event.Type == "error" && event.Message != "" {
				return events, errors.New(event.Message)
			}
		}
		return events, fmt.Errorf("agent runner failed: %s", strings.TrimSpace(string(stderrBytes)))
	}
	return events, nil
}

func resolveAgentRunnerPath() (string, error) {
	candidates := []string{
		filepath.Join("backend", "agent-runner", "dist", "index.js"),
		filepath.Join("agent-runner", "dist", "index.js"),
		filepath.Join("/pb", "agent-runner", "dist", "index.js"),
	}
	for _, candidate := range candidates {
		if _, err := os.Stat(candidate); err == nil {
			return candidate, nil
		}
	}
	return "", errors.New("agent runner is not built")
}

func loadAssistantHistory(app core.App, tripID, userID string) ([]assistantMessage, error) {
	records, err := app.FindAllRecords(
		"trip_assistant_messages",
		dbx.NewExp("trip = {:tripId} and user = {:userId}", dbx.Params{"tripId": tripID, "userId": userID}),
	)
	if err != nil {
		return nil, err
	}
	sort.Slice(records, func(i, j int) bool {
		return records[i].GetDateTime("created").Time().Before(records[j].GetDateTime("created").Time())
	})
	messages := make([]assistantMessage, 0, len(records))
	for _, record := range records {
		messages = append(messages, assistantMessage{Role: record.GetString("role"), Content: record.GetString("content")})
	}
	return messages, nil
}

func createAssistantProposalRecord(app core.App, trip *core.Record, userID string, event agentRunnerEvent) (*core.Record, error) {
	args, err := decodeProposalArguments(event.Arguments)
	if err != nil {
		return nil, err
	}
	if len(args.Changes) == 0 {
		return nil, errors.New("proposal contains no changes")
	}

	preview, err := buildProposalPreview(app, trip.Id, args)
	if err != nil {
		return nil, err
	}

	collection, err := app.FindCollectionByNameOrId("trip_assistant_proposals")
	if err != nil {
		return nil, err
	}
	record := core.NewRecord(collection)
	record.Set("trip", trip.Id)
	record.Set("user", userID)
	record.Set("status", "pending")
	record.Set("actionType", proposalActionType(args.Changes))
	record.Set("changes", args.Changes)
	record.Set("summary", firstNonEmpty(args.Summary, args.Title, "I have itinerary changes ready to review."))
	record.Set("preview", preview)
	record.Set("sources", event.Sources)
	record.Set("sdkState", event.SDKState)
	record.Set("sdkInterruptions", event.Interruptions)
	record.Set("expiresAt", time.Now().UTC().Add(proposalTTL).Format(time.RFC3339))
	if err := app.Save(record); err != nil {
		return nil, err
	}
	return record, nil
}

func decodeProposalArguments(raw interface{}) (assistantProposalArguments, error) {
	var args assistantProposalArguments
	switch value := raw.(type) {
	case string:
		if err := json.Unmarshal([]byte(value), &args); err != nil {
			return args, err
		}
	default:
		data, err := json.Marshal(value)
		if err != nil {
			return args, err
		}
		if err := json.Unmarshal(data, &args); err != nil {
			return args, err
		}
	}
	return args, validateProposalArguments(args)
}

func validateProposalArguments(args assistantProposalArguments) error {
	for i, change := range args.Changes {
		if _, err := collectionForEntity(change.EntityType); err != nil {
			return fmt.Errorf("change %d: %w", i+1, err)
		}
		switch change.Operation {
		case "create":
			if deref(change.RecordID) != "" {
				return fmt.Errorf("change %d: create must not include record_id", i+1)
			}
		case "update", "delete":
			if deref(change.RecordID) == "" {
				return fmt.Errorf("change %d: record_id is required", i+1)
			}
		default:
			return fmt.Errorf("change %d: unsupported operation", i+1)
		}
		if err := validateChangeTimes(change); err != nil {
			return fmt.Errorf("change %d: %w", i+1, err)
		}
	}
	return nil
}

func validateChangeTimes(change assistantChange) error {
	start := firstNonEmpty(stringValue(change.Fields["start_time"]), stringValue(change.Fields["departure_time"]))
	end := firstNonEmpty(stringValue(change.Fields["end_time"]), stringValue(change.Fields["arrival_time"]))
	if start != "" {
		if _, err := parseAssistantTime(start); err != nil {
			return fmt.Errorf("invalid start time: %w", err)
		}
	}
	if end != "" {
		if _, err := parseAssistantTime(end); err != nil {
			return fmt.Errorf("invalid end time: %w", err)
		}
	}
	if start != "" && end != "" {
		startTime, _ := parseAssistantTime(start)
		endTime, _ := parseAssistantTime(end)
		if !endTime.After(startTime) {
			return errors.New("end time must be after start time")
		}
	}
	return nil
}

func buildProposalPreview(app core.App, tripID string, args assistantProposalArguments) (map[string]interface{}, error) {
	changes := make([]map[string]interface{}, 0, len(args.Changes))
	for _, change := range args.Changes {
		if err := validateChangeWithinTrip(tripID, app, change); err != nil {
			return nil, err
		}
		collection, err := collectionForEntity(change.EntityType)
		if err != nil {
			return nil, err
		}
		item := map[string]interface{}{
			"operation":   change.Operation,
			"entity_type": change.EntityType,
			"record_id":   deref(change.RecordID),
			"reason":      deref(change.Reason),
			"confidence":  change.Confidence,
			"assumptions": change.Assumptions,
			"warnings":    change.Warnings,
		}
		switch change.Operation {
		case "create":
			after := previewFromChange(change)
			item["title"] = previewTitle(change.EntityType, after)
			item["after"] = after
		case "update":
			record, err := ensureTripRecord(app, collection, deref(change.RecordID), tripID)
			if err != nil {
				return nil, err
			}
			before := recordPreview(change.EntityType, record)
			after := cloneMap(before)
			applyPreviewChange(after, change)
			item["title"] = previewTitle(change.EntityType, after)
			item["before"] = before
			item["after"] = after
			item["diff"] = diffPreview(before, after)
		case "delete":
			record, err := ensureTripRecord(app, collection, deref(change.RecordID), tripID)
			if err != nil {
				return nil, err
			}
			before := recordPreview(change.EntityType, record)
			item["title"] = previewTitle(change.EntityType, before)
			item["before"] = before
		}
		changes = append(changes, item)
	}
	return map[string]interface{}{
		"title":       firstNonEmpty(args.Title, args.Summary, "Itinerary proposal"),
		"summary":     firstNonEmpty(args.Summary, args.Title, "Review these itinerary changes."),
		"assumptions": args.Assumptions,
		"warnings":    args.Warnings,
		"changes":     changes,
	}, nil
}

func validateChangeWithinTrip(tripID string, app core.App, change assistantChange) error {
	trip, err := app.FindRecordById("trips", tripID)
	if err != nil {
		return err
	}
	start := trip.GetDateTime("startDate").Time()
	end := trip.GetDateTime("endDate").Time()
	if start.IsZero() || end.IsZero() {
		return nil
	}
	start = start.AddDate(0, 0, -1)
	end = end.AddDate(0, 0, 1)
	for _, field := range []string{"start_time", "end_time", "departure_time", "arrival_time"} {
		value := stringValue(change.Fields[field])
		if value == "" {
			continue
		}
		t, err := parseAssistantTime(value)
		if err != nil {
			return err
		}
		if t.Before(start) || t.After(end) {
			return fmt.Errorf("%s is outside the trip date range", field)
		}
	}
	return nil
}

func resumeAssistantProposal(ctx context.Context, app core.App, tripID, userID string, proposal *core.Record, decision string) (string, error) {
	state := proposal.GetString("sdkState")
	if strings.TrimSpace(state) == "" {
		if decision == "approve" {
			return "Applied the approved itinerary changes.", nil
		}
		return "Okay, I will skip that change.", nil
	}
	events, err := invokeAgentRunner(ctx, app, nil, userID, agentRunnerInput{
		Mode:             "resume",
		Model:            tripAssistantModel,
		SDKState:         state,
		Decision:         decision,
		RejectionMessage: "The traveler rejected this itinerary proposal.",
	})
	if err != nil {
		return "", err
	}
	var text strings.Builder
	for _, event := range events {
		if event.Type == "text_delta" {
			text.WriteString(event.Text)
		}
		if event.Type == "error" {
			return "", errors.New(event.Message)
		}
	}
	_ = tripID
	return strings.TrimSpace(text.String()), nil
}

func proposalFromRecord(record *core.Record) assistantProposal {
	var changes []assistantChange
	var preview map[string]interface{}
	var sources []assistantSource
	var result map[string]interface{}
	_ = record.UnmarshalJSONField("changes", &changes)
	_ = record.UnmarshalJSONField("preview", &preview)
	_ = record.UnmarshalJSONField("sources", &sources)
	_ = record.UnmarshalJSONField("result", &result)
	return assistantProposal{
		Id:        record.Id,
		TripID:    record.GetString("trip"),
		UserID:    record.GetString("user"),
		Status:    record.GetString("status"),
		Action:    record.GetString("actionType"),
		Changes:   changes,
		Summary:   record.GetString("summary"),
		Preview:   preview,
		Sources:   sources,
		ExpiresAt: record.GetDateTime("expiresAt").Time().Format(time.RFC3339),
		Created:   record.GetDateTime("created").Time().Format(time.RFC3339),
		Updated:   record.GetDateTime("updated").Time().Format(time.RFC3339),
		Error:     record.GetString("error"),
		Result:    result,
	}
}

func requireProposalOwnership(record *core.Record, tripID, userID string) error {
	if record.GetString("trip") != tripID {
		return errors.New("proposal does not belong to this trip")
	}
	if record.GetString("user") != userID {
		return errors.New("proposal does not belong to this user")
	}
	return nil
}

func proposalExpired(record *core.Record) bool {
	expires := record.GetDateTime("expiresAt").Time()
	return !expires.IsZero() && time.Now().UTC().After(expires)
}

func markExpiredAssistantProposals(app core.App, tripID, userID string) error {
	records, err := app.FindAllRecords(
		"trip_assistant_proposals",
		dbx.NewExp("trip = {:tripId} and user = {:userId} and status = 'pending' and expiresAt < {:now}", dbx.Params{
			"tripId": tripID,
			"userId": userID,
			"now":    time.Now().UTC().Format(time.RFC3339),
		}),
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

func expirePendingAssistantProposals(app core.App, tripID, userID string) error {
	records, err := app.FindAllRecords(
		"trip_assistant_proposals",
		dbx.NewExp("trip = {:tripId} and user = {:userId} and status = 'pending'", dbx.Params{"tripId": tripID, "userId": userID}),
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

func CleanupExpiredTripAssistantProposals(app core.App) error {
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

func decodeAssistantSources(raw []interface{}) []assistantSource {
	sources := make([]assistantSource, 0, len(raw))
	for _, item := range raw {
		entry := mapValue(item)
		if url := stringValue(entry["url"]); url != "" {
			source := assistantSource{URL: url}
			if title := stringValue(entry["title"]); title != "" {
				source.Title = title
			}
			sources = append(sources, source)
		}
	}
	return sources
}

func collectionForEntity(entity string) (string, error) {
	switch entity {
	case "activity":
		return "activities", nil
	case "lodging":
		return "lodgings", nil
	case "transportation":
		return "transportations", nil
	default:
		return "", errors.New("unsupported entity type")
	}
}

func proposalFieldToPocketBase(entity, field string) string {
	fieldMap := map[string]string{
		"name":           "name",
		"description":    "description",
		"address":        "address",
		"type":           "type",
		"origin":         "origin",
		"destination":    "destination",
		"provider":       "provider",
		"start_time":     "startDate",
		"end_time":       "endDate",
		"departure_time": "departureTime",
		"arrival_time":   "arrivalTime",
		"confirmation":   "confirmationCode",
		"notes":          "notes",
		"link":           "link",
	}
	if entity == "activity" && field == "end_time" {
		return "endDate"
	}
	if entity == "lodging" && field == "start_time" {
		return "startDate"
	}
	if entity == "lodging" && field == "end_time" {
		return "endDate"
	}
	return fieldMap[field]
}

func timeFieldsForEntity(entity string) (string, string) {
	switch entity {
	case "activity", "lodging":
		return "startDate", "endDate"
	case "transportation":
		return "departureTime", "arrivalTime"
	default:
		return "", ""
	}
}

func parseAssistantTime(value string) (time.Time, error) {
	if t, err := time.Parse(time.RFC3339, value); err == nil {
		return t, nil
	}
	return time.Parse("2006-01-02T15:04:05", value)
}

func proposalActionType(changes []assistantChange) string {
	if len(changes) != 1 {
		return "batch"
	}
	return changes[0].Operation
}

func previewFromChange(change assistantChange) map[string]interface{} {
	preview := map[string]interface{}{}
	applyPreviewChange(preview, change)
	return preview
}

func recordPreview(entity string, record *core.Record) map[string]interface{} {
	switch entity {
	case "activity":
		return map[string]interface{}{
			"id":          record.Id,
			"name":        record.GetString("name"),
			"description": record.GetString("description"),
			"address":     record.GetString("address"),
			"start_time":  formatDate(record.GetDateTime("startDate")),
			"end_time":    formatDate(record.GetDateTime("endDate")),
			"notes":       record.GetString("notes"),
		}
	case "lodging":
		return map[string]interface{}{
			"id":           record.Id,
			"name":         record.GetString("name"),
			"type":         record.GetString("type"),
			"address":      record.GetString("address"),
			"start_time":   formatDate(record.GetDateTime("startDate")),
			"end_time":     formatDate(record.GetDateTime("endDate")),
			"confirmation": record.GetString("confirmationCode"),
			"notes":        record.GetString("notes"),
		}
	case "transportation":
		return map[string]interface{}{
			"id":             record.Id,
			"type":           record.GetString("type"),
			"origin":         record.GetString("origin"),
			"destination":    record.GetString("destination"),
			"provider":       record.GetString("provider"),
			"departure_time": formatDate(record.GetDateTime("departureTime")),
			"arrival_time":   formatDate(record.GetDateTime("arrivalTime")),
			"notes":          record.GetString("notes"),
		}
	default:
		return map[string]interface{}{"id": record.Id}
	}
}

func applyPreviewChange(preview map[string]interface{}, change assistantChange) {
	for _, field := range change.Clear {
		preview[field] = nil
	}
	for field, value := range change.Fields {
		if value != nil {
			preview[field] = value
		}
	}
}

func diffPreview(before, after map[string]interface{}) []map[string]interface{} {
	keys := map[string]bool{}
	for key := range before {
		keys[key] = true
	}
	for key := range after {
		keys[key] = true
	}
	diff := make([]map[string]interface{}, 0)
	for key := range keys {
		if fmt.Sprintf("%v", before[key]) != fmt.Sprintf("%v", after[key]) {
			diff = append(diff, map[string]interface{}{
				"field":  key,
				"before": before[key],
				"after":  after[key],
			})
		}
	}
	sort.Slice(diff, func(i, j int) bool {
		return stringValue(diff[i]["field"]) < stringValue(diff[j]["field"])
	})
	return diff
}

func previewTitle(entity string, values map[string]interface{}) string {
	switch entity {
	case "activity", "lodging":
		return firstNonEmpty(stringValue(values["name"]), "Untitled "+entity)
	case "transportation":
		label := strings.TrimSpace(fmt.Sprintf("%s from %s to %s", stringValue(values["type"]), stringValue(values["origin"]), stringValue(values["destination"])))
		return firstNonEmpty(label, "Transportation")
	default:
		return "Itinerary item"
	}
}

func cloneMap(input map[string]interface{}) map[string]interface{} {
	output := make(map[string]interface{}, len(input))
	for key, value := range input {
		output[key] = value
	}
	return output
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}

func deref(value *string) string {
	if value == nil {
		return ""
	}
	return strings.TrimSpace(*value)
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

func extractAssistantSources(value interface{}, seen map[string]bool) []assistantSource {
	sources := make([]assistantSource, 0)

	var walk func(interface{})
	walk = func(v interface{}) {
		switch current := v.(type) {
		case map[string]interface{}:
			if url := sourceURL(current); url != "" && !seen[url] {
				seen[url] = true
				sources = append(sources, assistantSource{
					Title: sourceTitle(current),
					URL:   url,
				})
			}
			for _, child := range current {
				walk(child)
			}
		case []interface{}:
			for _, child := range current {
				walk(child)
			}
		}
	}

	walk(value)
	return sources
}

func sourceURL(source map[string]interface{}) string {
	for _, key := range []string{"url", "uri", "link"} {
		if url := strings.TrimSpace(stringValue(source[key])); url != "" && strings.HasPrefix(url, "http") {
			return url
		}
	}
	return ""
}

func sourceTitle(source map[string]interface{}) string {
	for _, key := range []string{"title", "name", "text"} {
		if title := strings.TrimSpace(stringValue(source[key])); title != "" {
			return title
		}
	}
	return ""
}
