package routes

import "testing"

func TestValidateProposalArgumentsRequiresRecordForMutation(t *testing.T) {
	err := validateProposalArguments(assistantProposalArguments{
		Changes: []assistantChange{
			{
				Operation:  "update",
				EntityType: "activity",
				Fields:     map[string]interface{}{},
			},
		},
	})
	if err == nil {
		t.Fatal("expected update without record_id to fail")
	}
}

func TestValidateProposalArgumentsRejectsInvalidTimeRange(t *testing.T) {
	start := "2026-06-12T20:00:00Z"
	end := "2026-06-12T18:00:00Z"
	err := validateProposalArguments(assistantProposalArguments{
		Changes: []assistantChange{
			{
				Operation:  "create",
				EntityType: "activity",
				Fields: map[string]interface{}{
					"start_time": start,
					"end_time":   end,
				},
			},
		},
	})
	if err == nil {
		t.Fatal("expected end before start to fail")
	}
}

func TestAssistantTimeForStoragePreservesOffsetWallClock(t *testing.T) {
	stored, err := assistantTimeForStorage("2026-09-01T11:00:00-10:00")
	if err != nil {
		t.Fatal(err)
	}
	if stored != "2026-09-01T11:00:00Z" {
		t.Fatalf("expected wall-clock fake UTC time, got %q", stored)
	}
}

func TestParseAssistantTimeUsesWallClockForOffset(t *testing.T) {
	parsed, err := parseAssistantTime("2026-09-01T11:00:00-10:00")
	if err != nil {
		t.Fatal(err)
	}
	if parsed.Hour() != 11 || parsed.Location().String() != "UTC" {
		t.Fatalf("expected 11:00 UTC wall-clock time, got %s", parsed)
	}
}

func TestProposalActionTypeBatch(t *testing.T) {
	action := proposalActionType([]assistantChange{
		{Operation: "create", EntityType: "activity"},
		{Operation: "update", EntityType: "activity"},
	})
	if action != "batch" {
		t.Fatalf("expected batch action, got %q", action)
	}
}

func TestDiffPreview(t *testing.T) {
	diff := diffPreview(
		map[string]interface{}{"name": "Dinner", "address": "Old"},
		map[string]interface{}{"name": "Dinner", "address": "New"},
	)
	if len(diff) != 1 {
		t.Fatalf("expected one diff, got %d", len(diff))
	}
	if diff[0]["field"] != "address" {
		t.Fatalf("expected address diff, got %v", diff[0]["field"])
	}
}
