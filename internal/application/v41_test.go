package application

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"benzhi-project-cf63ae2b-9e6c-4d6e-b63f-f6a510693519/internal/domain"
)

func invalidationCase(at time.Time) (*domain.Case, domain.Qualification) {
	meter := qualification("meter-q", "meter-1", "current_meter", at)
	level := qualification("level-q", "level-1", "water_level_gauge", at)
	h, q := 1.2, 12.0
	evidence := domain.Evidence{EvidenceID: "measurement-1", EvidenceType: "rating_measurement", ObservedAt: at, WaterLevelM: &h, DischargeM3S: &q, SourceRef: "field/1", ContentDigest: domain.Digest("measurement"), QualityDecision: "included", Version: 1, InstrumentBindings: []domain.InstrumentBinding{binding(meter), binding(level)}}
	c := &domain.Case{CaseID: "invalidation-case", StationID: "station", RiverReach: "reach", CandidateVersion: "v1", OwnerID: "owner", ModelerID: "modeler", State: domain.Reviewed, Revision: 1, CreatedAt: at.Add(-24 * time.Hour), Evidence: []domain.Evidence{evidence}, Qualifications: []domain.Qualification{meter, level}, ReviewRounds: []domain.ReviewRound{{Round: 1, ReviewerID: "reviewer", Decision: "pass", Frozen: true}}, InstrumentInvalidations: []domain.InstrumentInvalidation{}, TrialExpirySettlements: []domain.TrialExpirySettlement{}}
	manifest := domain.BuildBaselineManifest(c, at.Add(-time.Hour))
	c.BaselineManifest, c.BaselineDigest = &manifest, manifest.Digest
	return c, meter
}

func TestInstrumentInvalidationUsesFrozenBindingsAndInvalidatesReview(t *testing.T) {
	ctx := context.Background()
	at := time.Date(2026, 7, 1, 0, 0, 0, 0, time.UTC)
	c, meter := invalidationCase(at)
	svc, store := testService(t, c)
	svc.now = func() time.Time { return at.Add(2 * time.Hour) }
	input := InstrumentInvalidationInput{Meta: Meta{RequestID: "invalidate", ActorID: "quality", ExpectedRevision: 1}, InvalidationType: "revoked", InvalidatedAt: at.Add(time.Hour), Reason: "校准机构撤销证书", NotificationEvidenceRef: "notice/1", OriginalCertificateDigest: meter.Digest}
	result, err := svc.InvalidateInstrument(ctx, c.CaseID, meter.QualificationID, input)
	if err != nil {
		t.Fatal(err)
	}
	var response InstrumentInvalidationResponse
	if err = json.Unmarshal(result.Body, &response); err != nil {
		t.Fatal(err)
	}
	if response.State != domain.Assessed || response.NextAction != "deviation_remediation" || len(response.AffectedBindings) != 1 || response.AffectedBindings[0].EvidenceID != "measurement-1" {
		t.Fatalf("失效影响响应不正确: %+v", response)
	}
	stored, err := store.LoadCase(ctx, c.CaseID)
	if err != nil {
		t.Fatal(err)
	}
	if len(stored.Deviations) != 1 || stored.Deviations[0].SourceGate != "instrument_certificate_invalidated" || !stored.ReviewRounds[0].Invalidated || stored.BaselineDigest != c.BaselineDigest {
		t.Fatalf("失效闭环或冻结基线不正确: %+v", stored)
	}
	replay, err := svc.InvalidateInstrument(ctx, c.CaseID, meter.QualificationID, input)
	if err != nil || !replay.Replayed || string(replay.Body) != string(result.Body) {
		t.Fatalf("失效通报幂等重放失败: %v", err)
	}
}

func trialSettlementCase(at time.Time, qualified bool) *domain.Case {
	until := at.Add(-time.Hour)
	decision := domain.Decision{DecisionID: "trial-1", DecisionType: "trial", CurveVersion: "v1", AuthorizedBy: "authority", EffectiveFrom: at.Add(-72 * time.Hour), EffectiveUntil: &until, LowerBoundM: 1, UpperBoundM: 4, RollbackCondition: "偏差超限", Status: "active"}
	items := []domain.TrialObservation{{ObservationID: "low", ObservedAt: at.Add(-60 * time.Hour), WaterLevelM: 1.1, MeasuredDischargeM3S: 10, PredictedDischargeM3S: 10, Verdict: "continue", SubmittedBy: "observer-a", Band: "low", CountsTowardProgress: true, RecordState: "active", TrialDecisionID: decision.DecisionID}, {ObservationID: "mid", ObservedAt: at.Add(-36 * time.Hour), WaterLevelM: 2.2, MeasuredDischargeM3S: 20, PredictedDischargeM3S: 20, Verdict: "continue", SubmittedBy: "observer-b", Band: "medium", CountsTowardProgress: true, RecordState: "active", TrialDecisionID: decision.DecisionID}, {ObservationID: "high", ObservedAt: at.Add(-6 * time.Hour), WaterLevelM: 3.8, MeasuredDischargeM3S: 30, PredictedDischargeM3S: 30, Verdict: "continue", SubmittedBy: "observer-b", Band: "high", CountsTowardProgress: true, RecordState: "active", TrialDecisionID: decision.DecisionID}}
	if !qualified {
		items = items[:2]
	}
	return &domain.Case{CaseID: "settlement-case", StationID: "station", RiverReach: "reach", CandidateVersion: "v1", OwnerID: "owner", State: domain.TrialActive, Revision: 1, CreatedAt: at.Add(-100 * time.Hour), Decisions: []domain.Decision{decision}, TrialObservations: items, InstrumentInvalidations: []domain.InstrumentInvalidation{}, TrialExpirySettlements: []domain.TrialExpirySettlement{}}
}

func TestTrialExpirySettlementQualifiesAndReplays(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 8, 1, 0, 0, 0, 0, time.UTC)
	c := trialSettlementCase(now, true)
	svc, store := testService(t, c)
	svc.now = func() time.Time { return now }
	result, err := svc.SettleTrialExpiry(ctx, c.CaseID, Meta{RequestID: "settle", ActorID: "authority", ExpectedRevision: 1})
	if err != nil {
		t.Fatal(err)
	}
	var response TrialExpirySettlementResponse
	if err = json.Unmarshal(result.Body, &response); err != nil {
		t.Fatal(err)
	}
	if response.Outcome != "qualified" || response.State != domain.TrialQualified || response.NextAction != "activation" || response.Settlement == nil || !response.Settlement.Progress.Qualified {
		t.Fatalf("合格结算响应不正确: %+v", response)
	}
	replay, err := svc.SettleTrialExpiry(ctx, c.CaseID, Meta{RequestID: "settle", ActorID: "authority", ExpectedRevision: 1})
	if err != nil || !replay.Replayed || string(replay.Body) != string(result.Body) {
		t.Fatalf("结算幂等重放失败: %v", err)
	}
	_ = store
}

func TestTrialExpiryBeforeDeadlineDoesNotChangeRevision(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 8, 1, 0, 0, 0, 0, time.UTC)
	c := trialSettlementCase(now, false)
	future := now.Add(time.Hour)
	c.Decisions[0].EffectiveUntil = &future
	svc, store := testService(t, c)
	svc.now = func() time.Time { return now }
	meta := Meta{RequestID: "early", ActorID: "authority", ExpectedRevision: 1}
	result, err := svc.SettleTrialExpiry(ctx, c.CaseID, meta)
	if err != nil {
		t.Fatal(err)
	}
	var response TrialExpirySettlementResponse
	if err = json.Unmarshal(result.Body, &response); err != nil {
		t.Fatal(err)
	}
	stored, err := store.LoadCase(ctx, c.CaseID)
	if err != nil {
		t.Fatal(err)
	}
	events, err := store.Events(ctx, c.CaseID)
	if err != nil {
		t.Fatal(err)
	}
	if response.Outcome != "trial_not_expired" || response.Revision != 1 || stored.Revision != 1 || len(events) != 0 || len(stored.TrialExpirySettlements) != 0 {
		t.Fatalf("截止前结算产生业务写入: response=%+v stored=%+v events=%d", response, stored, len(events))
	}
	replay, err := svc.SettleTrialExpiry(ctx, c.CaseID, meta)
	if err != nil || !replay.Replayed || string(replay.Body) != string(result.Body) {
		t.Fatalf("截止前结果重放失败: %v", err)
	}
}
