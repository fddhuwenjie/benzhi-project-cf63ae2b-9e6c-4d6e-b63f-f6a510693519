package audit

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"sort"
	"time"

	"benzhi-project-cf63ae2b-9e6c-4d6e-b63f-f6a510693519/internal/domain"
)

func canonical(v any) ([]byte, error) { return json.Marshal(v) }

func BuildEvent(caseID string, sequence int64, typ, actor, requestID string, at time.Time, payload any, previous string) (domain.AuditEvent, error) {
	b, err := canonical(payload)
	if err != nil {
		return domain.AuditEvent{}, err
	}
	material := struct {
		CaseID   string          `json:"case_id"`
		Sequence int64           `json:"sequence_no"`
		Type     string          `json:"event_type"`
		Actor    string          `json:"actor_id"`
		Request  string          `json:"request_id"`
		At       string          `json:"occurred_at"`
		Payload  json.RawMessage `json:"payload"`
		Previous string          `json:"previous_digest"`
	}{caseID, sequence, typ, actor, requestID, at.UTC().Format(time.RFC3339Nano), b, previous}
	raw, _ := canonical(material)
	h := sha256.Sum256(raw)
	return domain.AuditEvent{EventID: fmt.Sprintf("%s-%06d", caseID, sequence), CaseID: caseID, SequenceNo: sequence, EventType: typ, ActorID: actor, RequestID: requestID, OccurredAt: at.UTC(), Payload: b, PreviousDigest: previous, EventDigest: hex.EncodeToString(h[:])}, nil
}

func Verify(events []domain.AuditEvent) (bool, int, string) {
	previous := ""
	expected := int64(1)
	for i, event := range events {
		if event.SequenceNo != expected || event.PreviousDigest != previous {
			return false, i, "事件序号或前向摘要断裂"
		}
		copy, err := BuildEvent(event.CaseID, event.SequenceNo, event.EventType, event.ActorID, event.RequestID, event.OccurredAt, event.Payload, event.PreviousDigest)
		if err != nil || copy.EventDigest != event.EventDigest {
			return false, i, "事件载荷摘要不匹配"
		}
		previous = event.EventDigest
		expected++
	}
	return true, -1, "完整"
}

type Archive struct {
	Case      domain.Case         `json:"case"`
	Events    []domain.AuditEvent `json:"events"`
	Manifest  []ManifestItem      `json:"manifest"`
	Digest    string              `json:"archive_digest"`
	CreatedAt time.Time           `json:"created_at"`
}
type ManifestItem struct {
	Kind   string `json:"kind"`
	ID     string `json:"id"`
	Digest string `json:"digest"`
}

func addManifest(items *[]ManifestItem, kind, id string, v any) {
	*items = append(*items, ManifestItem{Kind: kind, ID: id, Digest: domain.Digest(v)})
}

// normalizeCaseSnapshot returns a snapshot of the case with the primary
// identifier-keyed collections sorted by their identifiers for stable archive
// digests. The input case is not mutated: every collection is copied before
// sorting so the caller's slice backing arrays and element order are preserved.
func normalizeCaseSnapshot(c *domain.Case) {
	c.Evidence = sortEvidenceByID(c.Evidence)
	c.Qualifications = sortQualificationsByID(c.Qualifications)
	c.Deviations = sortDeviationsByID(c.Deviations)
	c.Decisions = sortDecisionsByID(c.Decisions)
	c.TrialObservations = sortObservationsByID(c.TrialObservations)
}

func sortEvidenceByID(in []domain.Evidence) []domain.Evidence {
	out := append([]domain.Evidence(nil), in...)
	sort.Slice(out, func(i, j int) bool { return out[i].EvidenceID < out[j].EvidenceID })
	return out
}

func sortQualificationsByID(in []domain.Qualification) []domain.Qualification {
	out := append([]domain.Qualification(nil), in...)
	sort.Slice(out, func(i, j int) bool { return out[i].QualificationID < out[j].QualificationID })
	return out
}

func sortDeviationsByID(in []domain.Deviation) []domain.Deviation {
	out := append([]domain.Deviation(nil), in...)
	sort.Slice(out, func(i, j int) bool { return out[i].DeviationID < out[j].DeviationID })
	return out
}

func sortDecisionsByID(in []domain.Decision) []domain.Decision {
	out := append([]domain.Decision(nil), in...)
	sort.Slice(out, func(i, j int) bool { return out[i].DecisionID < out[j].DecisionID })
	return out
}

func sortObservationsByID(in []domain.TrialObservation) []domain.TrialObservation {
	out := append([]domain.TrialObservation(nil), in...)
	sort.Slice(out, func(i, j int) bool { return out[i].ObservationID < out[j].ObservationID })
	return out
}

func sortedEventsBySequence(in []domain.AuditEvent) []domain.AuditEvent {
	out := append([]domain.AuditEvent(nil), in...)
	sort.Slice(out, func(i, j int) bool { return out[i].SequenceNo < out[j].SequenceNo })
	return out
}

func BuildArchive(c domain.Case, events []domain.AuditEvent, at time.Time) (Archive, error) {
	normalizeCaseSnapshot(&c)
	events = sortedEventsBySequence(events)
	items := []ManifestItem{{Kind: "case_snapshot", ID: c.CaseID, Digest: domain.Digest(c)}}
	for _, e := range c.Evidence {
		items = append(items, ManifestItem{Kind: "evidence", ID: e.EvidenceID, Digest: e.ContentDigest})
	}
	for _, v := range c.EvidenceVersions {
		addManifest(&items, "evidence_version", fmt.Sprintf("%s/v%d", v.EvidenceID, v.Version), v)
	}
	for _, v := range c.QualificationVersions {
		addManifest(&items, "instrument_qualification_version", fmt.Sprintf("%s/v%d", v.QualificationID, v.Version), v)
	}
	if c.Assessment != nil {
		addManifest(&items, "assessment_run", c.Assessment.RunID, *c.Assessment)
	}
	for _, d := range c.Deviations {
		addManifest(&items, "deviation", d.DeviationID, d)
		for _, r := range d.Retests {
			addManifest(&items, "deviation_retest", d.DeviationID+"/"+r.RetestID, r)
		}
		for _, r := range d.DueDateRevisions {
			addManifest(&items, "deviation_due_date_revision", fmt.Sprintf("%s/v%d", d.DeviationID, r.Version), r)
		}
	}
	for _, r := range c.ReviewRounds {
		addManifest(&items, "review_round", fmt.Sprintf("%03d", r.Round), r)
	}
	for _, o := range c.TrialObservations {
		addManifest(&items, "trial_observation", o.ObservationID, o)
	}
	for _, x := range c.TrialSuspensions {
		addManifest(&items, "trial_suspension", x.SuspensionID, x)
		if x.Investigation != nil {
			addManifest(&items, "trial_suspension_investigation", x.SuspensionID, *x.Investigation)
		}
		for i, r := range x.RecoveryAttempts {
			addManifest(&items, "trial_recovery", fmt.Sprintf("%s/%03d", x.SuspensionID, i+1), r)
		}
	}
	for _, d := range c.Decisions {
		items = append(items, ManifestItem{Kind: "decision", ID: d.DecisionID, Digest: domain.Digest(d)})
	}
	for _, x := range c.InstrumentInvalidations {
		addManifest(&items, "instrument_invalidation", x.InvalidationID, x)
	}
	for _, x := range c.TrialExpirySettlements {
		addManifest(&items, "trial_expiry_settlement", x.SettlementID, x)
	}
	for _, e := range events {
		items = append(items, ManifestItem{Kind: "audit_event", ID: e.EventID, Digest: e.EventDigest})
	}
	sort.Slice(items, func(i, j int) bool {
		if items[i].Kind == items[j].Kind {
			return items[i].ID < items[j].ID
		}
		return items[i].Kind < items[j].Kind
	})
	body := struct {
		Case     domain.Case         `json:"case"`
		Events   []domain.AuditEvent `json:"events"`
		Manifest []ManifestItem      `json:"manifest"`
	}{c, events, items}
	return Archive{Case: c, Events: events, Manifest: items, Digest: domain.Digest(body), CreatedAt: at.UTC()}, nil
}

func VerifyArchive(a Archive) (bool, string) {
	ok, pos, msg := Verify(a.Events)
	if !ok {
		return false, fmt.Sprintf("审计链第 %d 项失败：%s", pos, msg)
	}
	body := struct {
		Case     domain.Case         `json:"case"`
		Events   []domain.AuditEvent `json:"events"`
		Manifest []ManifestItem      `json:"manifest"`
	}{a.Case, a.Events, a.Manifest}
	if domain.Digest(body) != a.Digest {
		return false, "档案总摘要不匹配"
	}
	return true, "完整"
}
