package store

import (
	"jobpilot/core/api/internal/models"
	"testing"
)

func historyFixture() models.ExternalApplicationInput {
	return models.ExternalApplicationInput{ExternalKey: "synthetic:receipt:1", Employer: "Excluded Example Ltd", Requisition: "REQ-1", CanonicalURL: "https://example.test/jobs/1", AppliedAt: "2024-02-02", OutcomeAt: "2024-02-09", Status: "rejected", Source: "manual synthetic archive", EvidenceRef: "archive:rejection-1", EvidenceKind: "rejection", CVVersion: "sha256:synthetic-v1"}
}
func TestValidateHistory(t *testing.T) {
	good := historyFixture()
	if e := ValidateHistory(good); e != nil {
		t.Fatal(e)
	}
	for _, change := range []func(*models.ExternalApplicationInput){
		func(x *models.ExternalApplicationInput) { x.ExternalKey = "" },
		func(x *models.ExternalApplicationInput) { x.Status = "ready" },
		func(x *models.ExternalApplicationInput) { x.AppliedAt = "yesterday" },
		func(x *models.ExternalApplicationInput) { x.OutcomeAt = "2024-01-01" },
		func(x *models.ExternalApplicationInput) { x.CanonicalURL = "javascript:alert(1)" },
		func(x *models.ExternalApplicationInput) { x.CanonicalURL = "https://secret:password@example.test" },
		func(x *models.ExternalApplicationInput) { x.EvidenceRef = "email body\nsecret" },
		func(x *models.ExternalApplicationInput) { x.EvidenceKind = "alert" },
		func(x *models.ExternalApplicationInput) { x.EvidenceKind = "generic_receipt"; x.Status = "applied" },
		func(x *models.ExternalApplicationInput) { x.EvidenceKind = "application_receipt" },
		func(x *models.ExternalApplicationInput) { x.CanonicalURL = "https://example.test/jobs/1#receipt" },
		func(x *models.ExternalApplicationInput) { x.CanonicalURL = "https://example.test/jobs/1#" },
		func(x *models.ExternalApplicationInput) {
			x.AppliedAt, x.OutcomeAt = "2024-02-05T00:00:00Z", "2024-02-02"
		},
		func(x *models.ExternalApplicationInput) {
			x.AppliedAt, x.OutcomeAt = "2024-02-05", "2024-02-02T00:00:00Z"
		},
		func(x *models.ExternalApplicationInput) {
			x.AppliedAt, x.OutcomeAt = "2024-02-02T15:00:00Z", "2024-02-02T16:00:00+02:00"
		},
		// The first instants at which a bare date is certainly the earlier one.
		func(x *models.ExternalApplicationInput) {
			x.AppliedAt, x.OutcomeAt = "2024-02-03T12:00:00Z", "2024-02-02"
		},
		func(x *models.ExternalApplicationInput) {
			x.AppliedAt, x.OutcomeAt = "2024-02-02", "2024-02-01T09:59:59Z"
		},
	} {
		bad := good
		change(&bad)
		if ValidateHistory(bad) == nil {
			t.Fatalf("accepted invalid history: %+v", bad)
		}
	}
	// A bare date beside a timestamp names a whole day in no zone: the same
	// day, or the day a zone away, does not establish that the outcome came first.
	for _, dates := range [][2]string{
		{"2024-02-02T15:00:00Z", "2024-02-02"},
		{"2024-02-02", "2024-02-02T01:00:00+02:00"},
		{"2024-02-02T23:30:00-08:00", "2024-02-02"},
		{"2024-02-02T15:00:00Z", "2024-02-02T15:00:00Z"},
		{"2024-02-03T11:59:59Z", "2024-02-02"},
		{"2024-02-02", "2024-02-01T10:00:00Z"},
	} {
		ok := good
		ok.AppliedAt, ok.OutcomeAt = dates[0], dates[1]
		if e := ValidateHistory(ok); e != nil {
			t.Fatal(dates, e)
		}
	}
	encoded := good
	encoded.CanonicalURL = "https://example.test/jobs/c%23-developer?ref=1"
	if e := ValidateHistory(encoded); e != nil {
		t.Fatal("an encoded # is part of the path", e)
	}
	good.AppliedAt = ""
	good.OutcomeAt = ""
	if e := ValidateHistory(good); e != nil {
		t.Fatal("unknown dates", e)
	}
	good.Status = "unknown"
	good.EvidenceKind = "generic_receipt"
	if e := ValidateHistory(good); e != nil {
		t.Fatal(e)
	}
}
