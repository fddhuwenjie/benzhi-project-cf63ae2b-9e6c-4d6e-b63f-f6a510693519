package application

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"testing"
	"time"

	"benzhi-project-cf63ae2b-9e6c-4d6e-b63f-f6a510693519/internal/domain"
	"benzhi-project-cf63ae2b-9e6c-4d6e-b63f-f6a510693519/internal/repository"
)

func testService(t *testing.T, c *domain.Case) (*Service, *repository.Store) {
	t.Helper()
	store, err := repository.Open(t.TempDir() + "/test.db")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { store.Close() })
	if err = store.Within(context.Background(), func(tx *repository.Tx) error { return tx.InsertCase(context.Background(), c) }); err != nil {
		t.Fatal(err)
	}
	return New(store), store
}
func qualification(id, instrument, kind string, at time.Time) domain.Qualification {
	q := domain.Qualification{QualificationID: id, InstrumentID: instrument, InstrumentKind: kind, CertificateRef: "cert-" + id, CalibratedAt: at.Add(-time.Hour), ValidUntil: at.Add(time.Hour), UsageStartedAt: at.Add(-time.Hour), UsageEndedAt: at.Add(time.Hour), Verdict: "qualified", Version: 1}
	q.Digest = domain.QualificationDigest(q)
	return q
}
func binding(q domain.Qualification) domain.InstrumentBinding {
	return domain.InstrumentBinding{InstrumentKind: q.InstrumentKind, InstrumentID: q.InstrumentID, QualificationID: q.QualificationID, CertificateDigest: q.Digest}
}

func TestEvidenceBatchAtomicAndIdempotent(t *testing.T) {
	ctx := context.Background()
	at := time.Date(2026, 7, 1, 0, 0, 0, 0, time.UTC)
	meter, level, survey := qualification("qm", "m", "current_meter", at), qualification("ql", "l", "water_level_gauge", at), qualification("qs", "s", "survey_equipment", at)
	c := &domain.Case{CaseID: "case", StationID: "station", RiverReach: "reach", CandidateVersion: "v1", OwnerID: "owner", State: domain.Draft, Revision: 1, CreatedAt: at, Qualifications: []domain.Qualification{meter, level, survey}, QualificationVersions: []domain.QualificationVersion{{QualificationID: meter.QualificationID, Version: 1, Digest: meter.Digest, Snapshot: meter}, {QualificationID: level.QualificationID, Version: 1, Digest: level.Digest, Snapshot: level}, {QualificationID: survey.QualificationID, Version: 1, Digest: survey.Digest, Snapshot: survey}}}
	svc, store := testService(t, c)
	h, q := 1.0, 0.0
	bad := EvidenceBatchInput{Meta: Meta{RequestID: "bad", ActorID: "a", ExpectedRevision: 1}, Evidence: []EvidenceBatchItem{{EvidenceID: "m1", EvidenceType: "rating_measurement", ObservedAt: at, WaterLevelM: &h, DischargeM3S: &q, SourceRef: "src", Content: "bad", QualityDecision: "included", InstrumentBindings: []domain.InstrumentBinding{binding(meter), binding(level)}}}}
	if _, err := svc.AddEvidenceBatch(ctx, "case", bad); err == nil {
		t.Fatal("零流量批次应失败")
	}
	stored, _ := store.LoadCase(ctx, "case")
	if stored.Revision != 1 || len(stored.Evidence) != 0 {
		t.Fatalf("失败批次发生部分写入: %+v", stored)
	}
	q = 10
	good := EvidenceBatchInput{Meta: Meta{RequestID: "good", ActorID: "a", ExpectedRevision: 1}, Evidence: []EvidenceBatchItem{{EvidenceID: "m1", EvidenceType: "rating_measurement", ObservedAt: at, WaterLevelM: &h, DischargeM3S: &q, SourceRef: "m", Content: "measurement", QualityDecision: "included", InstrumentBindings: []domain.InstrumentBinding{binding(meter), binding(level)}}, {EvidenceID: "x1", EvidenceType: "cross_section", ObservedAt: at, SourceRef: "x", Content: "section", QualityDecision: "included", InstrumentBindings: []domain.InstrumentBinding{binding(survey)}}, {EvidenceID: "f1", EvidenceType: "field_record", ObservedAt: at, SourceRef: "f", Content: "field", QualityDecision: "included", InstrumentBindings: []domain.InstrumentBinding{}}}}
	first, err := svc.AddEvidenceBatch(ctx, "case", good)
	if err != nil {
		t.Fatal(err)
	}
	replay, err := svc.AddEvidenceBatch(ctx, "case", good)
	if err != nil || !replay.Replayed || string(first.Body) != string(replay.Body) {
		t.Fatalf("批次重放不一致: %v", err)
	}
	var response EvidenceBatchResponse
	if err = json.Unmarshal(first.Body, &response); err != nil || response.Revision != 2 || response.Count != 3 {
		t.Fatalf("批次响应无效: %+v %v", response, err)
	}
	stored, _ = store.LoadCase(ctx, "case")
	events, _ := store.Events(ctx, "case")
	if stored.Revision != 2 || len(stored.Evidence) != 3 || len(events) != 1 || events[0].EventType != "evidence_batch_registered" {
		t.Fatalf("批次事务或审计不正确")
	}
	good.Evidence[0].Content = "different"
	if _, err = svc.AddEvidenceBatch(ctx, "case", good); !errors.Is(err, domain.ErrConflict) {
		t.Fatalf("相同 request_id 不同内容应冲突: %v", err)
	}
	preflight, err := svc.BaselinePreflight(ctx, "case")
	if err != nil {
		t.Fatal(err)
	}
	_, err = svc.AddEvidence(ctx, "case", EvidenceInput{Meta: Meta{RequestID: "later", ActorID: "a", ExpectedRevision: 2}, EvidenceID: "f2", EvidenceType: "field_record", ObservedAt: at, SourceRef: "f2", Content: "later", QualityDecision: "included"})
	if err != nil {
		t.Fatal(err)
	}
	_, err = svc.Freeze(ctx, "case", FreezeInput{Meta: Meta{RequestID: "stale-freeze", ActorID: "owner", ExpectedRevision: 3}, ProposedBaselineDigest: preflight.ProposedBaselineDigest})
	var stale *BaselineConflictError
	if !errors.As(err, &stale) || stale.Latest.Revision != 3 {
		t.Fatalf("陈旧清单应返回最新预检: %v", err)
	}
	preflight, err = svc.BaselinePreflight(ctx, "case")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = svc.Freeze(ctx, "case", FreezeInput{Meta: Meta{RequestID: "freeze", ActorID: "owner", ExpectedRevision: 3}, ProposedBaselineDigest: preflight.ProposedBaselineDigest}); err != nil {
		t.Fatal(err)
	}
	page1, err := svc.TimelinePage(ctx, "case", TimelineInput{ActorID: "a", Limit: 1})
	if err != nil {
		t.Fatal(err)
	}
	page2, err := svc.TimelinePage(ctx, "case", TimelineInput{ActorID: "a", Limit: 1, Cursor: page1.NextCursor})
	if err != nil {
		t.Fatal(err)
	}
	if !page1.PageIntegrity || !page2.PageIntegrity || len(page1.Events) != 1 || len(page2.Events) != 1 || page1.Events[0].SequenceNo >= page2.Events[0].SequenceNo {
		t.Fatalf("时间线分页断点无效: %+v %+v", page1, page2)
	}
	requestPage, err := svc.TimelinePage(ctx, "case", TimelineInput{RequestID: "good", Limit: 10})
	if err != nil || len(requestPage.Events) != 1 || requestPage.ResponseStatusDigest == "" {
		t.Fatalf("request_id 查询缺少响应状态证明: %+v %v", requestPage, err)
	}
}

func TestDeviationFailedRetestRequiresNewAttempt(t *testing.T) {
	ctx := context.Background()
	at := time.Now().UTC()
	d := domain.Deviation{DeviationID: "d", SourceGate: "residual_diagnostic", Severity: "minor", State: "analyzed", CreatedBy: "creator", CreatedAt: at, OriginalDueAt: at.Add(24 * time.Hour), DueAt: at.Add(24 * time.Hour), PhaseHistory: []domain.DeviationPhase{}, Retests: []domain.DeviationRetest{}, CorrectionAttempts: []domain.CorrectionAttempt{}}
	d.Containment, d.RootCause = "已遏制", "根因"
	c := &domain.Case{CaseID: "case", StationID: "station", RiverReach: "reach", CandidateVersion: "v", OwnerID: "owner", State: domain.Assessed, Revision: 1, CreatedAt: at, Deviations: []domain.Deviation{d}}
	svc, store := testService(t, c)
	res, err := svc.CorrectDeviation(ctx, "case", "d", DeviationStepInput{Meta: Meta{"c1", "fixer", 1}, Description: "第一轮整改", EvidenceRef: "e1"})
	if err != nil {
		t.Fatal(err)
	}
	var snap domain.Case
	json.Unmarshal(res.Body, &snap)
	res, err = svc.VerifyDeviation(ctx, "case", "d", DeviationRetestInput{Meta: Meta{"v1", "verifier", snap.Revision}, RetestID: "r1", VerifiedBy: "verifier", Actual: .2})
	if err != nil {
		t.Fatal(err)
	}
	json.Unmarshal(res.Body, &snap)
	if snap.Deviations[0].State != "correction_required" {
		t.Fatalf("复验失败未回退: %+v", snap.Deviations[0])
	}
	if _, err = svc.VerifyDeviation(ctx, "case", "d", DeviationRetestInput{Meta: Meta{"v2", "verifier", snap.Revision}, RetestID: "r2", VerifiedBy: "verifier", Actual: .01}); !errors.Is(err, domain.ErrGate) {
		t.Fatalf("同轮再次复验应拒绝: %v", err)
	}
	if _, err = svc.CorrectDeviation(ctx, "case", "d", DeviationStepInput{Meta: Meta{"c2", "fixer", snap.Revision}, Description: "第二轮针对超限调整", EvidenceRef: "e1"}); !errors.Is(err, domain.ErrGate) {
		t.Fatalf("同证据新轮次应拒绝: %v", err)
	}
	res, err = svc.CorrectDeviation(ctx, "case", "d", DeviationStepInput{Meta: Meta{"c3", "fixer", snap.Revision}, Description: "第二轮针对超限调整", EvidenceRef: "e2"})
	if err != nil {
		t.Fatal(err)
	}
	json.Unmarshal(res.Body, &snap)
	res, err = svc.VerifyDeviation(ctx, "case", "d", DeviationRetestInput{Meta: Meta{"v3", "other", snap.Revision}, RetestID: "r2", VerifiedBy: "other", Actual: .01})
	if err != nil {
		t.Fatal(err)
	}
	json.Unmarshal(res.Body, &snap)
	if snap.State != domain.DeviationsClosed || len(snap.Deviations[0].CorrectionAttempts) != 2 || snap.Deviations[0].CorrectionAttempts[0].Verification.Verdict != "fail" {
		t.Fatalf("整改历史或状态错误: %+v", snap.Deviations[0])
	}
	_ = store
}

func TestQualificationCorrectionMarksAllBoundEvidenceStale(t *testing.T) {
	ctx := context.Background()
	at := time.Date(2026, 7, 1, 0, 0, 0, 0, time.UTC)
	meter, level := qualification("qm", "meter", "current_meter", at), qualification("ql", "level", "water_level_gauge", at)
	c := &domain.Case{CaseID: "stale", StationID: "station", RiverReach: "reach", CandidateVersion: "v", OwnerID: "owner", State: domain.Draft, Revision: 1, CreatedAt: at, Qualifications: []domain.Qualification{meter, level}, QualificationVersions: []domain.QualificationVersion{{QualificationID: meter.QualificationID, Version: 1, Digest: meter.Digest, Snapshot: meter}, {QualificationID: level.QualificationID, Version: 1, Digest: level.Digest, Snapshot: level}}}
	for i := 0; i < 3; i++ {
		h, q := float64(i+1), float64((i+1)*10)
		e := domain.Evidence{EvidenceID: fmt.Sprintf("m%d", i+1), EvidenceType: "rating_measurement", ObservedAt: at, WaterLevelM: &h, DischargeM3S: &q, SourceRef: "src", ContentDigest: domain.Digest(i), QualityDecision: "included", Version: 1, InstrumentBindings: []domain.InstrumentBinding{binding(meter), binding(level)}}
		c.Evidence = append(c.Evidence, e)
		c.EvidenceVersions = append(c.EvidenceVersions, domain.EvidenceVersion{EvidenceID: e.EvidenceID, Version: 1, ContentDigest: e.ContentDigest, Snapshot: e})
	}
	svc, _ := testService(t, c)
	_, err := svc.CorrectQualification(ctx, "stale", "qm", QualificationCorrectionInput{Meta: Meta{RequestID: "correct-q", ActorID: "quality", ExpectedRevision: 1}, PreviousDigest: meter.Digest, CorrectionReason: "证书换版", InstrumentID: "meter", InstrumentKind: "current_meter", CertificateRef: "cert-new", CalibratedAt: meter.CalibratedAt, ValidUntil: meter.ValidUntil, UsageStartedAt: meter.UsageStartedAt, UsageEndedAt: meter.UsageEndedAt})
	if err != nil {
		t.Fatal(err)
	}
	p, err := svc.BaselinePreflight(ctx, "stale")
	if err != nil {
		t.Fatal(err)
	}
	affected := map[string]bool{}
	for _, x := range p.Issues {
		if x.Code == "stale_certificate_digest" {
			affected[x.EvidenceID] = true
		}
	}
	if len(affected) != 3 {
		t.Fatalf("证书换版应标出全部引用证据: %+v", p.Issues)
	}
}

func TestTrialReplacementRecalculatesQualification(t *testing.T) {
	ctx := context.Background()
	start := time.Now().UTC().Add(-72 * time.Hour)
	assessment := &domain.Assessment{RunID: "run", InputDigest: "digest", MethodVersion: "rating-power-v1", Parameters: map[string]float64{}, ResidualMetrics: map[string]float64{}, LowerBoundM: 1, UpperBoundM: 4, ExtrapolationRatio: 0, Verdict: "pass", CompletedAt: start}
	until := start.Add(30 * 24 * time.Hour)
	c := &domain.Case{CaseID: "trial", StationID: "station", RiverReach: "reach", CandidateVersion: "v", OwnerID: "owner", State: domain.TrialQualified, Revision: 1, CreatedAt: start, Assessment: assessment,
		Decisions:         []domain.Decision{{DecisionID: "decision", DecisionType: "trial", CurveVersion: "v", AuthorizedBy: "authority", EffectiveFrom: start.Add(-time.Hour), EffectiveUntil: &until, LowerBoundM: 1, UpperBoundM: 4, RollbackCondition: "偏差超限"}},
		TrialObservations: []domain.TrialObservation{{ObservationID: "low", ObservedAt: start, WaterLevelM: 1.1, MeasuredDischargeM3S: 10, PredictedDischargeM3S: 10, RelativeBias: 0, Verdict: "continue", SubmittedBy: "observer-a", Band: "low", CountsTowardProgress: true, RecordState: "active"}, {ObservationID: "mid", ObservedAt: start.Add(24 * time.Hour), WaterLevelM: 2.2, MeasuredDischargeM3S: 20, PredictedDischargeM3S: 20, RelativeBias: 0, Verdict: "continue", SubmittedBy: "observer-b", Band: "medium", CountsTowardProgress: true, RecordState: "active"}, {ObservationID: "high", ObservedAt: start.Add(48 * time.Hour), WaterLevelM: 3.8, MeasuredDischargeM3S: 30, PredictedDischargeM3S: 30, RelativeBias: 0, Verdict: "continue", SubmittedBy: "observer-a", Band: "high", CountsTowardProgress: true, RecordState: "active"}}}
	svc, _ := testService(t, c)
	res, err := svc.ReplaceTrialObservation(ctx, "trial", "high", ReplacementInput{Meta: Meta{RequestID: "replace", ActorID: "observer-b", ExpectedRevision: 1}, Reason: "原高水位记录转录错误", EvidenceRef: "proof/new", ObservationID: "replacement", ObservedAt: start.Add(48 * time.Hour), WaterLevelM: 2.3, MeasuredDischargeM3S: 22, PredictedDischargeM3S: 22})
	if err != nil {
		t.Fatal(err)
	}
	var response struct {
		Case     domain.Case          `json:"case"`
		Revision int64                `json:"revision"`
		State    domain.State         `json:"state"`
		Progress domain.TrialProgress `json:"progress"`
	}
	if err = json.Unmarshal(res.Body, &response); err != nil {
		t.Fatal(err)
	}
	got := response.Case
	if got.State != domain.TrialActive || got.TrialObservations[2].RecordState != "superseded" || got.TrialObservations[2].SupersededBy != "replacement" || len(got.TrialObservations) != 4 {
		t.Fatalf("替代关系或资格重算错误: %+v", got)
	}
	if response.Revision != 2 || response.State != domain.TrialActive || response.Progress.Qualified {
		t.Fatalf("替代响应未返回重算进度: %+v", response)
	}
}
