package application

import (
	"context"
	"fmt"
	"math"
	"strings"
	"time"

	"benzhi-project-cf63ae2b-9e6c-4d6e-b63f-f6a510693519/internal/assessment"
	"benzhi-project-cf63ae2b-9e6c-4d6e-b63f-f6a510693519/internal/domain"
	"benzhi-project-cf63ae2b-9e6c-4d6e-b63f-f6a510693519/internal/repository"
)

type EvidenceCorrectionInput struct {
	Meta
	ContentDigest        string                     `json:"content_digest"`
	CorrectionReason     string                     `json:"correction_reason"`
	EvidenceType         string                     `json:"evidence_type"`
	ObservedAt           time.Time                  `json:"observed_at"`
	WaterLevelM          *float64                   `json:"water_level_m"`
	DischargeM3S         *float64                   `json:"discharge_m3s"`
	SourceRef            string                     `json:"source_ref"`
	Content              string                     `json:"content"`
	QualityDecision      string                     `json:"quality_decision"`
	DecisionReason       string                     `json:"decision_reason"`
	FloodEventID         string                     `json:"flood_event_id"`
	VerticalUncertaintyM *float64                   `json:"vertical_uncertainty_m"`
	DatumID              string                     `json:"datum_id"`
	ConfidenceLevel      string                     `json:"confidence_level"`
	InstrumentBindings   []domain.InstrumentBinding `json:"instrument_bindings"`
}

func (s *Service) CorrectEvidence(ctx context.Context, caseID, evidenceID string, in EvidenceCorrectionInput) (Result, error) {
	fingerprint := domain.Digest(map[string]any{"content_digest": in.ContentDigest, "correction_reason": in.CorrectionReason, "evidence_type": in.EvidenceType, "observed_at": in.ObservedAt, "water_level_m": in.WaterLevelM, "discharge_m3s": in.DischargeM3S, "source_ref": in.SourceRef, "content": in.Content, "quality_decision": in.QualityDecision, "decision_reason": in.DecisionReason})
	return s.mutateCommand(ctx, caseID, in.Meta, "evidence_corrected", "evidence_corrected/"+evidenceID+"/"+fingerprint, func(c *domain.Case, _ *repository.Tx) error {
		if strings.TrimSpace(in.Content) == "" {
			return fmt.Errorf("%w: content 必填", domain.ErrInvalid)
		}
		replacement := domain.Evidence{EvidenceID: evidenceID, EvidenceType: in.EvidenceType, ObservedAt: in.ObservedAt, WaterLevelM: in.WaterLevelM, DischargeM3S: in.DischargeM3S, SourceRef: in.SourceRef, ContentDigest: domain.ContentDigest(in.Content), QualityDecision: in.QualityDecision, DecisionReason: in.DecisionReason, FloodEventID: in.FloodEventID, VerticalUncertaintyM: in.VerticalUncertaintyM, DatumID: in.DatumID, ConfidenceLevel: in.ConfidenceLevel, InstrumentBindings: in.InstrumentBindings}
		if err := domain.ValidateBindings(replacement, c.Qualifications, false); err != nil {
			return err
		}
		return c.CorrectEvidence(evidenceID, in.ContentDigest, in.CorrectionReason, in.ActorID, replacement, s.now())
	})
}

type BulkQualityInput struct {
	Meta
	Decisions []domain.QualityDecisionChange `json:"decisions"`
}

func (s *Service) BulkQuality(ctx context.Context, caseID string, in BulkQualityInput) (Result, error) {
	return s.mutateCommand(ctx, caseID, in.Meta, "quality_decisions_rejudged", "quality_decisions_rejudged/"+domain.Digest(in.Decisions), func(c *domain.Case, _ *repository.Tx) error {
		if len(in.Decisions) == 0 {
			return fmt.Errorf("%w: decisions 不得为空", domain.ErrInvalid)
		}
		old := map[string]string{}
		for _, e := range c.Evidence {
			old[e.EvidenceID] = e.QualityDecision
		}
		if err := c.ApplyQualityDecisions(in.Decisions); err != nil {
			return err
		}
		at := s.now()
		for _, x := range in.Decisions {
			c.QualityDecisionHistory = append(c.QualityDecisionHistory, domain.QualityDecisionRecord{EvidenceID: x.EvidenceID, OldDecision: old[x.EvidenceID], NewDecision: x.Decision, Reason: x.Reason, ActorID: in.ActorID, OccurredAt: at})
		}
		return nil
	})
}

func (s *Service) EvidenceVersions(ctx context.Context, caseID, evidenceID string) (map[string]any, error) {
	c, err := s.store.LoadCase(ctx, caseID)
	if err != nil {
		return nil, err
	}
	chain, err := c.EvidenceVersionChain(evidenceID)
	if err != nil {
		return nil, err
	}
	return map[string]any{"case_id": caseID, "evidence_id": evidenceID, "baseline_digest": c.BaselineDigest, "versions": chain}, nil
}
func (s *Service) QualityPreflight(ctx context.Context, caseID string) (assessment.QualityReport, error) {
	c, err := s.store.LoadCase(ctx, caseID)
	if err != nil {
		return assessment.QualityReport{}, err
	}
	return s.engine.EvaluateSamples(c.Evidence), nil
}
func (s *Service) CoverageMatrix(ctx context.Context, caseID string) (assessment.CoverageMatrix, error) {
	c, err := s.store.LoadCase(ctx, caseID)
	if err != nil {
		return assessment.CoverageMatrix{}, err
	}
	return s.engine.CoverageMatrix(c.Evidence, c.Qualifications), nil
}

type DiagnosticResult struct {
	MethodVersion string                       `json:"method_version"`
	InputDigest   string                       `json:"input_digest"`
	Parameters    map[string]float64           `json:"parameters"`
	Integrity     string                       `json:"integrity"`
	BandSummaries []domain.ResidualBandSummary `json:"band_summaries"`
	Details       []domain.ResidualDetail      `json:"details"`
	Influence     []domain.InfluenceDetail     `json:"influence"`
	FloodMarks    []domain.FloodMarkConstraint `json:"flood_marks"`
	Boundaries    []domain.BoundaryDiagnostic  `json:"boundaries"`
}

func (s *Service) Diagnostics(ctx context.Context, caseID, band, verdict, side string) (DiagnosticResult, error) {
	if band != "" && band != "low" && band != "medium" && band != "high" {
		return DiagnosticResult{}, fmt.Errorf("%w: band 无效", domain.ErrInvalid)
	}
	if verdict != "" && verdict != "pass" && verdict != "fail" {
		return DiagnosticResult{}, fmt.Errorf("%w: verdict 无效", domain.ErrInvalid)
	}
	if side != "" && side != "low" && side != "high" {
		return DiagnosticResult{}, fmt.Errorf("%w: side 无效", domain.ErrInvalid)
	}
	if cached, ok := s.diagnosticCache.Load(caseID); ok {
		return filterDiagnosticResult(cached.(DiagnosticResult), band, verdict, side), nil
	}
	c, err := s.store.LoadCase(ctx, caseID)
	if err != nil {
		return DiagnosticResult{}, err
	}
	if c.Assessment == nil {
		return DiagnosticResult{}, domain.ErrNotFound
	}
	a := c.Assessment
	if c.BaselineManifest == nil || c.BaselineManifest.Digest != c.BaselineDigest {
		return DiagnosticResult{}, fmt.Errorf("%w: baseline.evidence_digest", domain.ErrGate)
	}
	expected := assessment.AssessmentInputDigestV40(c, assessment.BoundaryRequest{RequestedLowerBoundM: a.RequestedLowerBoundM, RequestedUpperBoundM: a.RequestedUpperBoundM, MaxLowExtensionRatio: a.MaxLowExtensionRatio, MaxHighExtensionRatio: a.MaxHighExtensionRatio, HistoricalHighM: a.Parameters["historical_high_m"]})
	if expected != a.InputDigest {
		return DiagnosticResult{}, fmt.Errorf("%w: assessment.input_digest", domain.ErrGate)
	}
	frozen := map[string]bool{}
	for _, v := range c.EvidenceVersions {
		if v.IncludedInBaseline && v.BaselineQualityDecision == "included" {
			frozen[v.EvidenceID] = true
		}
	}
	// 兼容从早期快照迁移的数据，其冻结证据链为空时由当前冻结投影建立集合。
	if len(frozen) == 0 {
		for _, e := range c.Evidence {
			if e.QualityDecision == "included" {
				frozen[e.EvidenceID] = true
			}
		}
	}
	for _, d := range a.ResidualDetails {
		if !frozen[d.EvidenceID] {
			return DiagnosticResult{}, fmt.Errorf("%w: residual_details[%s].evidence_id", domain.ErrGate, d.EvidenceID)
		}
	}
	out := DiagnosticResult{
		MethodVersion: a.MethodVersion,
		InputDigest:   a.InputDigest,
		Parameters:    a.Parameters,
		Integrity:     "pass",
		BandSummaries: append([]domain.ResidualBandSummary(nil), a.BandSummaries...),
		Details:       append([]domain.ResidualDetail(nil), a.ResidualDetails...),
		Influence:     append([]domain.InfluenceDetail(nil), a.InfluenceDetails...),
		FloodMarks:    append([]domain.FloodMarkConstraint(nil), a.FloodMarkConstraints...),
		Boundaries:    append([]domain.BoundaryDiagnostic(nil), a.BoundaryDiagnostics...),
	}
	s.diagnosticCache.Store(caseID, out)
	return filterDiagnosticResult(out, band, verdict, side), nil
}

func filterDiagnosticResult(out DiagnosticResult, band, verdict, side string) DiagnosticResult {
	bandSummaries := out.BandSummaries[:0]
	for _, x := range out.BandSummaries {
		if band == "" || x.Band == band {
			bandSummaries = append(bandSummaries, x)
		}
	}
	out.BandSummaries = bandSummaries
	details := out.Details[:0]
	for _, x := range out.Details {
		if (band == "" || x.Band == band) && (verdict == "" || x.Verdict == verdict) {
			details = append(details, x)
		}
	}
	out.Details = details
	influence := out.Influence[:0]
	for _, x := range out.Influence {
		if verdict == "" || x.Verdict == verdict {
			influence = append(influence, x)
		}
	}
	out.Influence = influence
	boundaries := out.Boundaries[:0]
	for _, x := range out.Boundaries {
		if side == "" || x.Side == side {
			boundaries = append(boundaries, x)
		}
	}
	out.Boundaries = boundaries
	return out
}

type DeviationStepInput struct {
	Meta
	Description string `json:"description"`
	EvidenceRef string `json:"evidence_ref"`
}
type DeviationRetestInput struct {
	Meta
	RetestID   string  `json:"retest_id"`
	VerifiedBy string  `json:"verified_by"`
	Actual     float64 `json:"actual"`
	Threshold  float64 `json:"threshold"`
}

func deviation(c *domain.Case, id string) (*domain.Deviation, error) {
	for i := range c.Deviations {
		if c.Deviations[i].DeviationID == id {
			return &c.Deviations[i], nil
		}
	}
	return nil, domain.ErrNotFound
}
func (s *Service) stepDeviation(ctx context.Context, caseID, id, target, event string, in DeviationStepInput) (Result, error) {
	return s.mutateCommand(ctx, caseID, in.Meta, event, event+"/"+id, func(c *domain.Case, _ *repository.Tx) error {
		if err := c.RequireState(domain.Assessed); err != nil {
			return err
		}
		d, err := deviation(c, id)
		if err != nil {
			return err
		}
		required := map[string]string{"contained": "open", "analyzed": "contained", "corrected": "analyzed"}
		allowedPrior := required[target]
		if target == "corrected" && d.State == "correction_required" {
			allowedPrior = "correction_required"
		}
		if d.State != allowedPrior {
			return fmt.Errorf("%w: 偏差阶段必须由 %s 推进到 %s", domain.ErrGate, required[target], target)
		}
		if strings.TrimSpace(in.Description) == "" {
			return fmt.Errorf("%w: description 必填", domain.ErrInvalid)
		}
		if (target == "contained" || target == "analyzed") && (d.Severity == "major" || d.Severity == "critical") && strings.TrimSpace(in.Description) == "" {
			return fmt.Errorf("%w: 重大偏差说明必填", domain.ErrInvalid)
		}
		if target == "corrected" && strings.TrimSpace(in.EvidenceRef) == "" {
			return fmt.Errorf("%w: 整改证据引用必填", domain.ErrInvalid)
		}
		if target == "contained" {
			d.Containment = in.Description
		}
		if target == "analyzed" {
			d.RootCause = in.Description
		}
		if target == "corrected" {
			if s.now().After(d.DueAt) && len(d.DueDateRevisions) == 0 {
				return fmt.Errorf("%w: overdue_due_date_revision_required", domain.ErrGate)
			}
			refs := []string{in.EvidenceRef}
			if len(d.CorrectionAttempts) > 0 {
				last := d.CorrectionAttempts[len(d.CorrectionAttempts)-1]
				if domain.SameStringSet(last.EvidenceRefs, refs) {
					return fmt.Errorf("%w: 新一轮整改必须引用不同证据", domain.ErrGate)
				}
				if strings.TrimSpace(in.Description) == strings.TrimSpace(last.Description) {
					return fmt.Errorf("%w: 必须说明针对上一轮失败原因的变化", domain.ErrGate)
				}
			}
			d.CorrectiveAction = in.Description
			d.VerificationEvidenceRef = in.EvidenceRef
			d.CorrectedBy = in.ActorID
			d.CorrectionAttempts = append(d.CorrectionAttempts, domain.CorrectionAttempt{AttemptNo: len(d.CorrectionAttempts) + 1, Description: in.Description, EvidenceRefs: refs, CorrectedBy: in.ActorID, CorrectedAt: s.now(), FailureChange: func() string {
				if len(d.Retests) > 0 {
					return d.Retests[len(d.Retests)-1].FailureReason
				}
				return ""
			}()})
			if d.SourceGate == "instrument_certificate_invalidated" {
				for i := range c.InstrumentInvalidations {
					if c.InstrumentInvalidations[i].DeviationID == d.DeviationID {
						c.InstrumentInvalidations[i].RestorationEvidenceRef = in.EvidenceRef
						c.InstrumentInvalidations[i].CoverageRestored = false
					}
				}
			}
		}
		d.State = target
		d.PhaseHistory = append(d.PhaseHistory, domain.DeviationPhase{State: target, ActorID: in.ActorID, OccurredAt: s.now(), Description: in.Description, EvidenceRef: in.EvidenceRef})
		return nil
	})
}
func (s *Service) ContainDeviation(ctx context.Context, c, id string, in DeviationStepInput) (Result, error) {
	return s.stepDeviation(ctx, c, id, "contained", "deviation_contained", in)
}
func (s *Service) AnalyzeDeviation(ctx context.Context, c, id string, in DeviationStepInput) (Result, error) {
	return s.stepDeviation(ctx, c, id, "analyzed", "deviation_analyzed", in)
}
func (s *Service) CorrectDeviation(ctx context.Context, c, id string, in DeviationStepInput) (Result, error) {
	return s.stepDeviation(ctx, c, id, "corrected", "deviation_corrected", in)
}
func (s *Service) VerifyDeviation(ctx context.Context, caseID, id string, in DeviationRetestInput) (Result, error) {
	return s.mutateCommand(ctx, caseID, in.Meta, "deviation_retested", "deviation_retested/"+id+"/"+in.RetestID, func(c *domain.Case, _ *repository.Tx) error {
		if err := c.RequireState(domain.Assessed); err != nil {
			return err
		}
		d, err := deviation(c, id)
		if err != nil {
			return err
		}
		if d.State != "corrected" {
			return fmt.Errorf("%w: 只有 corrected 偏差可复验", domain.ErrGate)
		}
		if s.now().After(d.DueAt) {
			d.EverOverdue = true
			if len(d.DueDateRevisions) == 0 {
				return fmt.Errorf("%w: overdue_due_date_revision_required", domain.ErrGate)
			}
		}
		if in.VerifiedBy == "" || in.VerifiedBy != in.ActorID || in.VerifiedBy == d.CreatedBy || in.VerifiedBy == d.CorrectedBy {
			return fmt.Errorf("%w: 复验人必须与创建人和整改人隔离", domain.ErrGate)
		}
		threshold, thresholdErr := s.engine.RetestThreshold(d.SourceGate, in.Threshold)
		if thresholdErr != nil {
			return thresholdErr
		}
		if math.IsNaN(in.Actual) || math.IsInf(in.Actual, 0) {
			return fmt.Errorf("%w: 复验实际值或门槛无效", domain.ErrInvalid)
		}
		verdict := "pass"
		reason := ""
		if math.Abs(in.Actual) > threshold {
			verdict = "fail"
			reason = "实际值超过定向复验门槛"
		}
		rule := d.SourceGate
		d.Retests = append(d.Retests, domain.DeviationRetest{RetestID: in.RetestID, Rule: rule, Actual: in.Actual, Threshold: threshold, Verdict: verdict, VerifiedBy: in.VerifiedBy, VerifiedAt: s.now(), FailureReason: reason})
		if len(d.CorrectionAttempts) == 0 {
			return fmt.Errorf("%w: 整改轮次缺失", domain.ErrGate)
		}
		d.CorrectionAttempts[len(d.CorrectionAttempts)-1].Verification = &d.Retests[len(d.Retests)-1]
		d.RetestVerdict = verdict
		if verdict == "pass" {
			if d.SourceGate == "instrument_certificate_invalidated" {
				matched := false
				for i := range c.InstrumentInvalidations {
					record := &c.InstrumentInvalidations[i]
					if record.DeviationID != d.DeviationID {
						continue
					}
					if strings.TrimSpace(record.RestorationEvidenceRef) == "" {
						return fmt.Errorf("%w: replacement_certificate_or_remeasurement_evidence_required", domain.ErrGate)
					}
					bindings, coverageErr := s.engine.InvalidatedBindings(c, record.QualificationID, record.InvalidationType)
					if coverageErr != nil || !sameInstrumentImpacts(bindings, record.AffectedBindings) {
						return fmt.Errorf("%w: affected_binding_retest_incomplete", domain.ErrGate)
					}
					record.CoverageRestored = true
					matched = true
				}
				if !matched {
					return fmt.Errorf("%w: instrument_invalidation_record_missing", domain.ErrGate)
				}
			}
			at := s.now()
			d.State = "verified"
			d.VerifiedBy = in.VerifiedBy
			d.VerifiedAt = &at
			d.PhaseHistory = append(d.PhaseHistory, domain.DeviationPhase{State: "verified", ActorID: in.VerifiedBy, OccurredAt: at, Description: "定向复验通过"})
			if c.AllDeviationsClosed() {
				return c.Advance(domain.DeviationsClosed)
			}
		} else {
			d.State = "correction_required"
			d.PhaseHistory = append(d.PhaseHistory, domain.DeviationPhase{State: "correction_required", ActorID: in.VerifiedBy, OccurredAt: s.now(), Description: reason})
		}
		return nil
	})
}

func sameInstrumentImpacts(actual, expected []domain.InstrumentImpact) bool {
	if len(actual) != len(expected) {
		return false
	}
	keys := map[string]bool{}
	for _, item := range expected {
		keys[item.EvidenceID+"/"+item.InstrumentKind+"/"+item.QualificationID] = true
	}
	for _, item := range actual {
		if !keys[item.EvidenceID+"/"+item.InstrumentKind+"/"+item.QualificationID] {
			return false
		}
	}
	return true
}
func (s *Service) Deviations(ctx context.Context, caseID, state, severity string) ([]domain.Deviation, error) {
	if state != "" && state != "open" && state != "contained" && state != "analyzed" && state != "corrected" && state != "correction_required" && state != "verified" {
		return nil, fmt.Errorf("%w: state 无效", domain.ErrInvalid)
	}
	if severity != "" && severity != "minor" && severity != "major" && severity != "critical" {
		return nil, fmt.Errorf("%w: severity 无效", domain.ErrInvalid)
	}
	c, err := s.store.LoadCase(ctx, caseID)
	if err != nil {
		return nil, err
	}
	out := []domain.Deviation{}
	for _, d := range c.Deviations {
		if (state == "" || d.State == state) && (severity == "" || d.Severity == severity) {
			out = append(out, d)
		}
	}
	return out, nil
}

type ReviewIssueResponseInput struct {
	Meta
	Response    string `json:"response"`
	EvidenceRef string `json:"evidence_ref"`
}
type ReviewResubmitInput struct {
	Meta
	ReviewerID string `json:"reviewer_id"`
}

func (s *Service) RespondReviewIssue(ctx context.Context, caseID, issueID string, in ReviewIssueResponseInput) (Result, error) {
	return s.mutateCommand(ctx, caseID, in.Meta, "review_issue_responded", "review_issue_responded/"+issueID, func(c *domain.Case, _ *repository.Tx) error {
		if len(c.ReviewRounds) == 0 {
			return domain.ErrNotFound
		}
		r := &c.ReviewRounds[len(c.ReviewRounds)-1]
		if r.Decision != "return" || r.Frozen {
			return fmt.Errorf("%w: 当前复核轮次不可整改", domain.ErrGate)
		}
		for i := range r.Issues {
			if r.Issues[i].IssueID == issueID {
				if strings.TrimSpace(in.Response) == "" || strings.TrimSpace(in.EvidenceRef) == "" {
					return fmt.Errorf("%w: response 和 evidence_ref 必填", domain.ErrInvalid)
				}
				at := s.now()
				r.Issues[i].Response = in.Response
				r.Issues[i].EvidenceRef = in.EvidenceRef
				r.Issues[i].RespondedBy = in.ActorID
				r.Issues[i].RespondedAt = &at
				return nil
			}
		}
		return domain.ErrNotFound
	})
}
func (s *Service) ResubmitReview(ctx context.Context, caseID string, in ReviewResubmitInput) (Result, error) {
	return s.mutate(ctx, caseID, in.Meta, "review_resubmitted", func(c *domain.Case, _ *repository.Tx) error {
		if len(c.ReviewRounds) == 0 {
			return domain.ErrNotFound
		}
		last := &c.ReviewRounds[len(c.ReviewRounds)-1]
		if last.Decision != "return" || last.Frozen {
			return fmt.Errorf("%w: 当前轮次不可重提", domain.ErrGate)
		}
		for _, x := range last.Issues {
			if x.RespondedAt == nil {
				return fmt.Errorf("%w: 复核问题 %s 尚未响应", domain.ErrGate, x.IssueID)
			}
		}
		if in.ReviewerID == "" || in.ReviewerID == c.OwnerID || in.ReviewerID == c.ModelerID {
			return fmt.Errorf("%w: 新复核员未通过职责隔离", domain.ErrGate)
		}
		last.Frozen = true
		c.ReviewRounds = append(c.ReviewRounds, domain.ReviewRound{Round: last.Round + 1, ReviewerID: in.ReviewerID, Decision: "pending", Issues: []domain.ReviewIssue{}})
		c.Review = nil
		return nil
	})
}
func (s *Service) ReviewHistory(ctx context.Context, caseID string) ([]domain.ReviewRound, error) {
	c, err := s.store.LoadCase(ctx, caseID)
	if err != nil {
		return nil, err
	}
	return c.ReviewRounds, nil
}

type InvestigationInput struct {
	Meta
	CauseCategory   string    `json:"cause_category"`
	ImpactStartedAt time.Time `json:"impact_started_at"`
	ImpactEndedAt   time.Time `json:"impact_ended_at"`
	Action          string    `json:"action"`
	EvidenceRef     string    `json:"evidence_ref"`
}
type RecoveryInput struct {
	Meta
	ReviewerID            string    `json:"reviewer_id"`
	ObservationID         string    `json:"observation_id"`
	ObservedAt            time.Time `json:"observed_at"`
	WaterLevelM           float64   `json:"water_level_m"`
	MeasuredDischargeM3S  float64   `json:"measured_discharge_m3s"`
	PredictedDischargeM3S float64   `json:"predicted_discharge_m3s"`
	BoundaryUnchanged     bool      `json:"boundary_unchanged"`
}

func activeSuspension(c *domain.Case) (*domain.TrialSuspension, error) {
	for i := len(c.TrialSuspensions) - 1; i >= 0; i-- {
		if c.TrialSuspensions[i].State == "active" {
			return &c.TrialSuspensions[i], nil
		}
	}
	return nil, domain.ErrNotFound
}
func (s *Service) SubmitInvestigation(ctx context.Context, caseID string, in InvestigationInput) (Result, error) {
	return s.mutate(ctx, caseID, in.Meta, "trial_suspension_investigated", func(c *domain.Case, _ *repository.Tx) error {
		if err := c.RequireState(domain.TrialSuspended); err != nil {
			return err
		}
		x, err := activeSuspension(c)
		if err != nil {
			return err
		}
		if in.CauseCategory == "" || in.Action == "" || in.EvidenceRef == "" || in.ImpactStartedAt.IsZero() || in.ImpactEndedAt.Before(in.ImpactStartedAt) {
			return fmt.Errorf("%w: 调查分类、影响时段、措施和证据引用必须完整", domain.ErrInvalid)
		}
		x.Investigation = &domain.SuspensionInvestigation{CauseCategory: in.CauseCategory, ImpactStartedAt: in.ImpactStartedAt, ImpactEndedAt: in.ImpactEndedAt, Action: in.Action, EvidenceRef: in.EvidenceRef, SubmittedBy: in.ActorID, SubmittedAt: s.now()}
		return nil
	})
}
func (s *Service) RecoverTrial(ctx context.Context, caseID string, in RecoveryInput) (Result, error) {
	return s.mutate(ctx, caseID, in.Meta, "trial_recovery_decided", func(c *domain.Case, _ *repository.Tx) error {
		if err := c.RequireState(domain.TrialSuspended); err != nil {
			return err
		}
		x, err := activeSuspension(c)
		if err != nil {
			return err
		}
		failure := ""
		if x.Investigation == nil {
			failure = "suspension_investigation_missing"
		} else if in.ReviewerID == "" || in.ReviewerID != in.ActorID || in.ReviewerID == x.ObservationActorID || in.ReviewerID == x.Investigation.SubmittedBy {
			failure = "duty_separation_failed"
		}
		for _, actor := range trialRestrictedActors(c) {
			if in.ReviewerID == actor {
				failure = "duty_separation_failed"
			}
		}
		for _, existing := range c.TrialObservations {
			if existing.ObservationID == in.ObservationID || (existing.ObservedAt.Equal(in.ObservedAt) && math.Abs(existing.WaterLevelM-in.WaterLevelM) < 1e-9) {
				failure = "duplicate_trial_observation"
			}
		}
		var trial *domain.Decision
		for i := range c.Decisions {
			if c.Decisions[i].DecisionType == "trial" {
				trial = &c.Decisions[i]
			}
		}
		if failure == "" && (trial == nil || trial.EffectiveUntil == nil || !s.now().Before(*trial.EffectiveUntil)) {
			failure = "trial_expired"
		}
		if failure == "" && !in.BoundaryUnchanged {
			failure = "applicability_boundary_changed"
		}
		o, obsErr := s.engine.TrialObservation(in.ObservationID, in.ObservedAt, in.WaterLevelM, in.MeasuredDischargeM3S, in.PredictedDischargeM3S)
		if failure == "" && obsErr != nil {
			return obsErr
		}
		if failure == "" && o.Verdict != "continue" {
			failure = "confirmation_threshold_failed"
		}
		verdict := "pass"
		if failure != "" {
			verdict = "fail"
		}
		x.RecoveryAttempts = append(x.RecoveryAttempts, domain.RecoveryAttempt{ReviewerID: in.ReviewerID, ConfirmationObservationID: in.ObservationID, Verdict: verdict, FailureReason: failure, DecidedAt: s.now()})
		if failure == "trial_expired" {
			x.State = "terminated"
		}
		if verdict == "pass" {
			o.SubmittedBy = in.ActorID
			o.RecoveryConfirmation = true
			o.CountsTowardProgress = true
			o.Band = assessment.TrialBand(c.Assessment, o.WaterLevelM)
			c.TrialObservations = append(c.TrialObservations, o)
			x.State = "recovered"
			c.State = domain.TrialActive
			refreshTrialContributions(c)
			if s.engine.TrialProgress(c).Qualified {
				c.State = domain.TrialQualified
			}
		}
		return nil
	})
}
func (s *Service) Suspensions(ctx context.Context, caseID string) ([]domain.TrialSuspension, error) {
	c, err := s.store.LoadCase(ctx, caseID)
	if err != nil {
		return nil, err
	}
	return c.TrialSuspensions, nil
}

type ActivationPreflight struct {
	Eligible             bool              `json:"eligible"`
	BlockingCodes        []string          `json:"blocking_codes"`
	CandidateVersion     string            `json:"candidate_version"`
	LowerBoundM          float64           `json:"lower_bound_m"`
	UpperBoundM          float64           `json:"upper_bound_m"`
	CurrentVersion       map[string]string `json:"current_version"`
	CurrentVersionDigest string            `json:"current_version_digest"`
	PlannedEffectiveFrom time.Time         `json:"planned_effective_from"`
	ReplacesVersion      string            `json:"replaces_version"`
}

func (s *Service) ActivationPreflight(ctx context.Context, caseID string, effective time.Time) (ActivationPreflight, error) {
	c, err := s.store.LoadCase(ctx, caseID)
	if err != nil {
		return ActivationPreflight{}, err
	}
	current, err := s.store.CurvePointer(ctx, c.StationID)
	if err != nil {
		return ActivationPreflight{}, err
	}
	out := ActivationPreflight{Eligible: true, BlockingCodes: []string{}, CandidateVersion: c.CandidateVersion, CurrentVersion: current, CurrentVersionDigest: domain.Digest(current), PlannedEffectiveFrom: effective}
	if c.Assessment != nil {
		out.LowerBoundM = c.Assessment.LowerBoundM
		out.UpperBoundM = c.Assessment.UpperBoundM
	}
	out.ReplacesVersion = current["curve_version"]
	if c.State != domain.TrialQualified {
		out.BlockingCodes = append(out.BlockingCodes, "trial_not_qualified")
	}
	for _, x := range c.TrialSuspensions {
		if x.State == "active" {
			out.BlockingCodes = append(out.BlockingCodes, "trial_suspension_active")
		}
	}
	if effective.IsZero() {
		out.BlockingCodes = append(out.BlockingCodes, "effective_from_required")
	}
	if v := current["effective_from"]; v != "" {
		at, _ := time.Parse(time.RFC3339Nano, v)
		if !effective.After(at) {
			out.BlockingCodes = append(out.BlockingCodes, "effective_time_conflict")
		}
	}
	out.Eligible = len(out.BlockingCodes) == 0
	return out, nil
}
func (s *Service) CurveHistory(ctx context.Context, station string) ([]map[string]any, error) {
	return s.store.CurveHistory(ctx, station)
}
func (s *Service) CurveAsOf(ctx context.Context, station string, at time.Time) (map[string]any, error) {
	if at.IsZero() {
		return nil, fmt.Errorf("%w: as_of 必须为 RFC3339 时刻", domain.ErrInvalid)
	}
	return s.store.CurveAsOf(ctx, station, at)
}
