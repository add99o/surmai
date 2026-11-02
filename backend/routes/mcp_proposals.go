package routes

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/pocketbase/pocketbase/core"
)

// MCP proposals share the native review UI, but carry a baseline to reject stale
// writes and run their entire batch and status update in one database transaction.
func createMCPProposal(app core.App, user, trip *core.Record, in mcpProposalInput) (*core.Record, error) {
	if len(in.Changes) < 1 || len(in.Changes) > 25 {
		return nil, errors.New("provide 1 to 25 changes")
	}
	if strings.TrimSpace(in.Summary) == "" || len(in.Summary) > 4000 || len(in.Title) > 200 {
		return nil, errors.New("a summary of at most 4000 characters and title of at most 200 characters are required")
	}
	args := assistantProposalArguments{Title: in.Title, Summary: in.Summary, Changes: in.Changes}
	if err := validateProposalArguments(args); err != nil {
		return nil, err
	}
	var proposal *core.Record
	err := app.RunInTransaction(func(tx core.App) error {
		currentTrip, err := mcpAccessibleTrip(tx, user, trip.Id, true)
		if err != nil {
			return err
		}
		baseline, err := mcpProposalBaseline(tx, user, currentTrip, args.Changes)
		if err != nil {
			return err
		}
		proposal, err = createAssistantProposalRecord(tx, currentTrip, user.Id, agentRunnerEvent{Arguments: args})
		if err != nil {
			return err
		}
		var preview map[string]any
		if err := proposal.UnmarshalJSONField("preview", &preview); err != nil {
			return err
		}
		preview["origin"] = "mcp"
		preview["baseline"] = baseline
		proposal.Set("preview", preview)
		return tx.Save(proposal)
	})
	return proposal, err
}

func mcpRecordHash(r *core.Record) string {
	data, _ := json.Marshal(r.PublicExport())
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

func mcpProposalBaseline(app core.App, user, trip *core.Record, changes []assistantChange) (map[string]string, error) {
	baseline := map[string]string{"trips/" + trip.Id: mcpRecordHash(trip)}
	seen := map[string]bool{}
	for _, change := range changes {
		collectionName, err := collectionForEntity(change.EntityType)
		if err != nil {
			return nil, err
		}
		for field := range change.Fields {
			// Costs use dedicated expense records in current Surmai; this release
			// exposes expense reading and leaves expense editing to the native UI.
			if field == "cost_value" || field == "cost_currency" {
				return nil, errors.New("edit expenses in Surmai; MCP itinerary proposals do not change costs")
			}
			if !mcpAllowedField(change.EntityType, field) {
				return nil, fmt.Errorf("unsupported field: %s", field)
			}
			if field == "metadata" && len(mapValue(change.Fields[field])) == 0 {
				return nil, errors.New("metadata must be a nonempty object")
			}
		}
		for _, field := range change.Clear {
			if field == "metadata" || !mcpAllowedField(change.EntityType, field) {
				return nil, fmt.Errorf("unsupported clear field: %s", field)
			}
		}
		var record *core.Record
		method := "POST"
		if change.Operation == "create" {
			collection, err := app.FindCollectionByNameOrId(collectionName)
			if err != nil {
				return nil, err
			}
			record = core.NewRecord(collection)
			record.Set("trip", trip.Id)
		} else {
			record, err = ensureTripRecord(app, collectionName, deref(change.RecordID), trip.Id)
			if err != nil {
				return nil, errors.New("target record unavailable in this trip")
			}
			if !mcpCanAccess(app, user, record, false) {
				return nil, errors.New("target record is not readable by this user")
			}
			key := collectionName + "/" + record.Id
			if seen[key] {
				return nil, errors.New("combine multiple changes to the same record into one change")
			}
			seen[key] = true
			baseline[key] = mcpRecordHash(record)
			method = "PATCH"
			if change.Operation == "delete" {
				method = "DELETE"
			}
		}
		rule := record.Collection().CreateRule
		if method == "PATCH" {
			rule = record.Collection().UpdateRule
		}
		if method == "DELETE" {
			rule = record.Collection().DeleteRule
		}
		if method != "DELETE" {
			if err := applyChangeFields(record, change, method == "POST"); err != nil {
				return nil, err
			}
		}
		allowed := false
		if method == "POST" {
			allowed = mcpCanCreate(app, user, record)
		} else {
			allowed, err = app.CanAccessRecord(record, &core.RequestInfo{Auth: user, Method: method, Body: record.PublicExport()}, rule)
		}
		if err != nil || !allowed {
			return nil, errors.New("target operation is not permitted for this user")
		}
	}
	return baseline, nil
}

func mcpAllowedField(entity, field string) bool {
	fields := map[string]string{
		"activity":       " name description address start_time end_time link metadata ",
		"lodging":        " name type address start_time end_time confirmation link metadata ",
		"transportation": " type origin destination departure_time arrival_time notes link metadata ",
	}
	return field != "" && strings.Contains(fields[entity], " "+field+" ")
}

func isMCPProposal(proposal *core.Record) bool {
	var preview map[string]any
	return proposal.UnmarshalJSONField("preview", &preview) == nil && preview["origin"] == "mcp"
}

func approveMCPProposal(app core.App, trip *core.Record, userID string, proposal *core.Record) (string, error) {
	var result map[string]any
	err := app.RunInTransaction(func(tx core.App) error {
		fresh, err := tx.FindRecordById("trip_assistant_proposals", proposal.Id)
		if err != nil {
			return err
		}
		if err := requireProposalOwnership(fresh, trip.Id, userID); err != nil {
			return err
		}
		if !isMCPProposal(fresh) || (fresh.GetString("status") != "pending" && fresh.GetString("status") != "failed") || proposalExpired(fresh) {
			return errors.New("proposal is no longer pending or has expired")
		}
		user, err := tx.FindRecordById("users", userID)
		if err != nil {
			return err
		}
		currentTrip, err := mcpAccessibleTrip(tx, user, trip.Id, true)
		if err != nil {
			return err
		}
		var changes []assistantChange
		if err := fresh.UnmarshalJSONField("changes", &changes); err != nil {
			return err
		}
		baseline, err := mcpProposalBaseline(tx, user, currentTrip, changes)
		if err != nil {
			return err
		}
		var preview struct {
			Baseline map[string]string `json:"baseline"`
		}
		if err := fresh.UnmarshalJSONField("preview", &preview); err != nil {
			return err
		}
		if len(preview.Baseline) != len(baseline) {
			return errors.New("proposal baseline unavailable; create a new proposal")
		}
		for key, hash := range baseline {
			if preview.Baseline[key] != hash {
				return errors.New("trip or target records changed since preview; create a new proposal")
			}
		}
		result, err = applyAssistantProposalBatch(tx, trip.Id, fresh)
		if err != nil {
			return err
		}
		fresh.Set("status", "approved")
		fresh.Set("result", result)
		fresh.Set("error", "")
		return tx.Save(fresh)
	})
	if err != nil {
		return "", err
	}
	proposal.Set("status", "approved")
	proposal.Set("result", result)
	proposal.Set("error", "")
	return result["message"].(string), nil
}
