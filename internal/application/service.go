package application

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"sort"
	"strings"
	"sync"
	"time"

	"benzhi-project-cf63ae2b-9e6c-4d6e-b63f-f6a510693519/internal/assessment"
	"benzhi-project-cf63ae2b-9e6c-4d6e-b63f-f6a510693519/internal/audit"
	"benzhi-project-cf63ae2b-9e6c-4d6e-b63f-f6a510693519/internal/domain"
	"benzhi-project-cf63ae2b-9e6c-4d6e-b63f-f6a510693519/internal/repository"
)

type Service struct {
	store         *repository.Store
	engine        *assessment.Engine
	now           func() time.Time
	locks         sync.Map
	reviewHistory sync.Map
}
type Result struct {
	Status   int
	Body     []byte
	Replayed bool
}
type Meta struct {
	RequestID        string `json:"request_id"`
	ActorID          string `json:"actor_id"`
	ExpectedRevision int64  `json:"expected_revision"`
}

func New(store *repository.Store) *Service {
	return &Service{store: store, engine: assessment.New(), now: func() time.Time { return time.Now().UTC() }}
}
func (s *Service) Store() *repository.Store { return s.store }
func (s *Service) stationLock(key string) *sync.Mutex {
	v, _ := s.locks.LoadOrStore(key, &sync.Mutex{})
	return v.(*sync.Mutex)
}
func validateMeta(m Meta) error {
	if strings.TrimSpace(m.RequestID) == "" || len(m.RequestID) > 120 {
		return fmt.Errorf("%w: request_id 必填且不超过 120 字符", domain.ErrInvalid)
	}
	if strings.TrimSpace(m.ActorID) == "" || len(m.ActorID) > 80 {
		return fmt.Errorf("%w: actor_id 必填且不超过 80 字符", domain.ErrInvalid)
	}
	if m.ExpectedRevision < 0 {
		return fmt.Errorf("%w: expected_revision 不得为负数", domain.ErrInvalid)
	}
	return nil
}
func marshal(v any) []byte { b, _ := json.Marshal(v); return b }

type CreateInput struct {
	Meta
	CaseID           string `json:"case_id"`
	StationID        string `json:"station_id"`
	RiverReach       string `json:"river_reach"`
	CandidateVersion string `json:"candidate_version"`
	OwnerID          string `json:"owner_id"`
}

func (s *Service) Create(ctx context.Context, in CreateInput) (Result, error) {
	if err := validateMeta(in.Meta); err != nil {
		return Result{}, err
	}
	if in.ExpectedRevision != 0 {
		return Result{}, fmt.Errorf("%w: 新建案件 expected_revision 必须为 0", domain.ErrConflict)
	}
	c := &domain.Case{CaseID: in.CaseID, StationID: in.StationID, RiverReach: in.RiverReach, CandidateVersion: in.CandidateVersion, OwnerID: in.OwnerID, State: domain.Draft, Revision: 1, CreatedAt: s.now(), Evidence: []domain.Evidence{}, Qualifications: []domain.Qualification{}, Deviations: []domain.Deviation{}, Decisions: []domain.Decision{}, TrialObservations: []domain.TrialObservation{}, EvidenceVersions: []domain.EvidenceVersion{}, QualificationVersions: []domain.QualificationVersion{}, ReviewRounds: []domain.ReviewRound{}, TrialSuspensions: []domain.TrialSuspension{}, QualityDecisionHistory: []domain.QualityDecisionRecord{}, InstrumentInvalidations: []domain.InstrumentInvalidation{}, TrialExpirySettlements: []domain.TrialExpirySettlement{}}
	if err := domain.ValidateCreate(c); err != nil {
		return Result{}, err
	}
	if err := c.ValidateConsistency(); err != nil {
		return Result{}, err
	}
	lock := s.stationLock(c.StationID)
	lock.Lock()
	defer lock.Unlock()
	var out Result
	err := s.store.Within(ctx, func(tx *repository.Tx) error {
		storedCaseID, command, status, body, ok, err := tx.GetIdempotent(ctx, in.RequestID)
		if err != nil {
			return err
		}
		if ok {
			if storedCaseID != c.CaseID || (command != "" && command != "case_created") {
				return fmt.Errorf("%w: request_id 已用于不同命令", domain.ErrConflict)
			}
			out = Result{Status: status, Body: body, Replayed: true}
			return nil
		}
		if existing, found, checkErr := tx.ActiveCandidate(ctx, c.StationID, c.CandidateVersion); checkErr != nil {
			return checkErr
		} else if found {
			return &domain.CandidateConflictError{CaseID: existing.CaseID, State: existing.State, Revision: existing.Revision}
		}
		if err = tx.InsertCase(ctx, c); err != nil {
			return err
		}
		event, err := audit.BuildEvent(c.CaseID, 1, "case_created", in.ActorID, in.RequestID, s.now(), c, "")
		if err != nil {
			return err
		}
		if err = tx.AppendEvent(ctx, event); err != nil {
			return err
		}
		body = marshal(c)
		if err = tx.SaveIdempotent(ctx, in.RequestID, c.CaseID, "case_created", 201, body); err != nil {
			return err
		}
		out = Result{Status: 201, Body: body}
		return nil
	})
	return out, err
}

type EvidenceInput struct {
	Meta
	EvidenceID           string                     `json:"evidence_id"`
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
type QualificationInput struct {
	Meta
	QualificationID string    `json:"qualification_id"`
	InstrumentID    string    `json:"instrument_id"`
	InstrumentKind  string    `json:"instrument_kind"`
	CertificateRef  string    `json:"certificate_ref"`
	CalibratedAt    time.Time `json:"calibrated_at"`
	ValidUntil      time.Time `json:"valid_until"`
	UsageStartedAt  time.Time `json:"usage_started_at"`
	UsageEndedAt    time.Time `json:"usage_ended_at"`
}
type AssessmentInput struct {
	Meta
	RunID                 string  `json:"run_id"`
	HistoricalHighM       float64 `json:"historical_high_m"`
	MaxExtensionRatio     float64 `json:"max_extension_ratio"`
	RequestedLowerBoundM  float64 `json:"requested_lower_bound_m"`
	RequestedUpperBoundM  float64 `json:"requested_upper_bound_m"`
	MaxLowExtensionRatio  float64 `json:"max_low_extension_ratio"`
	MaxHighExtensionRatio float64 `json:"max_high_extension_ratio"`
}

type FreezeInput struct {
	Meta
	ProposedBaselineDigest string `json:"proposed_baseline_digest"`
}
type DeviationInput struct {
	Meta
	DeviationID string `json:"deviation_id"`
	SourceGate  string `json:"source_gate"`
	Severity    string `json:"severity"`
}
type RemediationInput struct {
	Meta
	Containment             string `json:"containment"`
	RootCause               string `json:"root_cause"`
	CorrectiveAction        string `json:"corrective_action"`
	VerificationEvidenceRef string `json:"verification_evidence_ref"`
	VerifiedBy              string `json:"verified_by"`
	RetestPassed            bool   `json:"retest_passed"`
}
type ReviewInput struct {
	Meta
	ReviewerID      string               `json:"reviewer_id"`
	Decision        string               `json:"decision"`
	Comment         string               `json:"comment"`
	Issues          []domain.ReviewIssue `json:"issues"`
	MaterialsDigest string               `json:"materials_digest"`
}
type TrialInput struct {
	Meta
	DecisionID        string    `json:"decision_id"`
	AuthorizedBy      string    `json:"authorized_by"`
	EffectiveFrom     time.Time `json:"effective_from"`
	EffectiveUntil    time.Time `json:"effective_until"`
	RollbackCondition string    `json:"rollback_condition"`
}
type ObservationInput struct {
	Meta
	ObservationID         string    `json:"observation_id"`
	ObservedAt            time.Time `json:"observed_at"`
	WaterLevelM           float64   `json:"water_level_m"`
	MeasuredDischargeM3S  float64   `json:"measured_discharge_m3s"`
	PredictedDischargeM3S float64   `json:"predicted_discharge_m3s"`
}
type ActivationInput struct {
	Meta
	DecisionID           string    `json:"decision_id"`
	AuthorizedBy         string    `json:"authorized_by"`
	EffectiveFrom        time.Time `json:"effective_from"`
	RollbackCondition    string    `json:"rollback_condition"`
	CurrentVersionDigest string    `json:"current_version_digest"`
}

type mutation func(*domain.Case, *repository.Tx) error

func (s *Service) mutate(ctx context.Context, caseID string, meta Meta, eventType string, fn mutation) (Result, error) {
	return s.mutateCommand(ctx, caseID, meta, eventType, eventType, fn)
}
func (s *Service) mutateCommand(ctx context.Context, caseID string, meta Meta, eventType, commandType string, fn mutation) (Result, error) {
	if err := validateMeta(meta); err != nil {
		return Result{}, err
	}
	if caseID == "" {
		return Result{}, fmt.Errorf("%w: case_id 必填", domain.ErrInvalid)
	}
	stationID, err := s.store.CaseStation(ctx, caseID)
	if err != nil {
		return Result{}, err
	}
	lock := s.stationLock(stationID)
	lock.Lock()
	defer lock.Unlock()
	var out Result
	err = s.store.Within(ctx, func(tx *repository.Tx) error {
		storedCaseID, command, status, body, ok, err := tx.GetIdempotent(ctx, meta.RequestID)
		if err != nil {
			return err
		}
		if ok {
			if storedCaseID != caseID || (command != "" && command != commandType) {
				return fmt.Errorf("%w: request_id 已用于不同命令", domain.ErrConflict)
			}
			out = Result{Status: status, Body: body, Replayed: true}
			return nil
		}
		c, err := tx.LoadCase(ctx, caseID)
		if err != nil {
			return err
		}
		if c.Revision != meta.ExpectedRevision {
			if eventType == "baseline_frozen" {
				return &BaselineConflictError{Latest: s.buildBaselinePreflight(c)}
			}
			return fmt.Errorf("%w: 当前 revision 为 %d", domain.ErrConflict, c.Revision)
		}
		if err = c.EnsureMutable(); err != nil {
			return err
		}
		previous := c.Revision
		if err = fn(c, tx); err != nil {
			return err
		}
		c.Revision++
		if err = c.ValidateConsistency(); err != nil {
			return err
		}
		events, err := tx.Events(ctx, caseID)
		if err != nil {
			return err
		}
		prevDigest := ""
		if len(events) > 0 {
			prevDigest = events[len(events)-1].EventDigest
		}
		event, err := audit.BuildEvent(caseID, int64(len(events)+1), eventType, meta.ActorID, meta.RequestID, s.now(), c, prevDigest)
		if err != nil {
			return err
		}
		if err = tx.AppendEvent(ctx, event); err != nil {
			return err
		}
		if err = tx.SaveCase(ctx, c, previous); err != nil {
			return err
		}
		if eventType == "trial_observation_replaced" {
			body = marshal(struct {
				Case     *domain.Case         `json:"case"`
				Revision int64                `json:"revision"`
				State    domain.State         `json:"state"`
				Progress domain.TrialProgress `json:"progress"`
			}{c, c.Revision, c.State, s.engine.TrialProgress(c)})
		} else {
			body = marshal(c)
		}
		if err = tx.SaveIdempotent(ctx, meta.RequestID, caseID, commandType, 200, body); err != nil {
			return err
		}
		out = Result{Status: 200, Body: body}
		return nil
	})
	return out, err
}

func (s *Service) AddEvidence(ctx context.Context, id string, in EvidenceInput) (Result, error) {
	return s.mutateCommand(ctx, id, in.Meta, "evidence_registered", "evidence_registered/"+in.EvidenceID, func(c *domain.Case, _ *repository.Tx) error {
		if err := c.RequireState(domain.Draft); err != nil {
			return err
		}
		if in.EvidenceID == "" || in.SourceRef == "" || in.Content == "" {
			return fmt.Errorf("%w: evidence_id、source_ref、content 必填", domain.ErrInvalid)
		}
		allowed := map[string]bool{"rating_measurement": true, "cross_section": true, "field_record": true, "historical_flood_mark": true}
		if !allowed[in.EvidenceType] {
			return fmt.Errorf("%w: 不支持的 evidence_type", domain.ErrInvalid)
		}
		for _, e := range c.Evidence {
			if e.EvidenceID == in.EvidenceID {
				return fmt.Errorf("%w: evidence_id 重复", domain.ErrConflict)
			}
		}
		decision := in.QualityDecision
		if decision == "" {
			decision = "pending"
		}
		if decision != "pending" && decision != "included" && decision != "excluded" {
			return fmt.Errorf("%w: quality_decision 无效", domain.ErrInvalid)
		}
		if decision == "excluded" && in.DecisionReason == "" {
			return fmt.Errorf("%w: 排除证据必须说明原因", domain.ErrInvalid)
		}
		evidence := domain.Evidence{EvidenceID: in.EvidenceID, EvidenceType: in.EvidenceType, ObservedAt: in.ObservedAt, WaterLevelM: in.WaterLevelM, DischargeM3S: in.DischargeM3S, SourceRef: in.SourceRef, ContentDigest: domain.ContentDigest(in.Content), QualityDecision: decision, DecisionReason: in.DecisionReason, Version: 1, FloodEventID: in.FloodEventID, VerticalUncertaintyM: in.VerticalUncertaintyM, DatumID: in.DatumID, ConfidenceLevel: in.ConfidenceLevel, InstrumentBindings: in.InstrumentBindings}
		if err := domain.ValidateEvidence(evidence); err != nil {
			return err
		}
		if err := domain.ValidateBindings(evidence, c.Qualifications, false); err != nil {
			return err
		}
		c.Evidence = append(c.Evidence, evidence)
		c.EvidenceVersions = append(c.EvidenceVersions, domain.EvidenceVersion{EvidenceID: evidence.EvidenceID, Version: 1, ContentDigest: evidence.ContentDigest, ActorID: in.ActorID, CreatedAt: s.now(), Snapshot: evidence})
		return nil
	})
}
func (s *Service) AddQualification(ctx context.Context, id string, in QualificationInput) (Result, error) {
	return s.mutateCommand(ctx, id, in.Meta, "instrument_qualified", "instrument_qualified/"+in.QualificationID, func(c *domain.Case, _ *repository.Tx) error {
		if err := c.RequireState(domain.Draft); err != nil {
			return err
		}
		q := domain.Qualification{QualificationID: in.QualificationID, InstrumentID: in.InstrumentID, InstrumentKind: in.InstrumentKind, CertificateRef: in.CertificateRef, CalibratedAt: in.CalibratedAt, ValidUntil: in.ValidUntil, UsageStartedAt: in.UsageStartedAt, UsageEndedAt: in.UsageEndedAt, Version: 1}
		s.engine.QualifyInstrument(&q)
		q.Digest = domain.QualificationDigest(q)
		if err := domain.ValidateQualification(q); err != nil {
			return err
		}
		c.Qualifications = append(c.Qualifications, q)
		c.QualificationVersions = append(c.QualificationVersions, domain.QualificationVersion{QualificationID: q.QualificationID, Version: 1, Digest: q.Digest, ActorID: in.ActorID, CreatedAt: s.now(), Verdict: q.Verdict, Snapshot: q})
		return nil
	})
}
func (s *Service) Freeze(ctx context.Context, id string, in FreezeInput) (Result, error) {
	return s.mutate(ctx, id, in.Meta, "baseline_frozen", func(c *domain.Case, _ *repository.Tx) error {
		preflight := s.buildBaselinePreflight(c)
		if in.ProposedBaselineDigest == "" {
			return fmt.Errorf("%w: proposed_baseline_digest 必填", domain.ErrInvalid)
		}
		if in.ProposedBaselineDigest != preflight.ProposedBaselineDigest || in.ExpectedRevision != preflight.Revision {
			return &BaselineConflictError{Latest: preflight}
		}
		if len(preflight.FreezeBlockingCodes) > 0 {
			return &domain.StructuredError{Kind: domain.ErrGate, Issues: preflight.Issues}
		}
		manifest := domain.BuildBaselineManifest(c, s.now())
		manifest.GateCodes = []string{"required_evidence_complete", "version_chains_complete", "instrument_bindings_complete"}
		c.BaselineManifest = &manifest
		c.BaselineDigest = manifest.Digest
		c.MarkBaselineVersions()
		return c.Advance(domain.BaselineFrozen)
	})
}
func (s *Service) Qualify(ctx context.Context, id string, m Meta) (Result, error) {
	return s.mutate(ctx, id, m, "evidence_qualified", func(c *domain.Case, _ *repository.Tx) error {
		evidence, err := c.FrozenEvidence()
		if err != nil {
			return err
		}
		qualifications, err := c.FrozenQualifications()
		if err != nil {
			return err
		}
		report := s.engine.EvaluateSamples(evidence)
		if report.Verdict != "pass" {
			return fmt.Errorf("%w: 样本质量规则未通过: %v", domain.ErrGate, report.Issues)
		}
		matrix := s.engine.CoverageMatrix(evidence, qualifications)
		if matrix.Verdict != "pass" {
			return fmt.Errorf("%w: instrument_coverage: %v", domain.ErrGate, matrix.BlockingCodes)
		}
		return c.QualifyEvidence()
	})
}
func (s *Service) Assess(ctx context.Context, id string, in AssessmentInput) (Result, error) {
	return s.mutateCommand(ctx, id, in.Meta, "assessment_completed", "assessment_completed/"+in.RunID, func(c *domain.Case, _ *repository.Tx) error {
		if in.RunID == "" || in.ActorID == c.OwnerID {
			return fmt.Errorf("%w: 建模人必须填写且不得是案件责任人", domain.ErrGate)
		}
		lowRatio, highRatio := in.MaxLowExtensionRatio, in.MaxHighExtensionRatio
		if highRatio == 0 {
			highRatio = in.MaxExtensionRatio
		}
		if lowRatio == 0 {
			lowRatio = highRatio
		}
		evidence, ferr := c.FrozenEvidence()
		if ferr != nil {
			return ferr
		}
		original := c.Evidence
		c.Evidence = evidence
		defer func() { c.Evidence = original }()
		points := []float64{}
		for _, x := range evidence {
			if x.EvidenceType == "rating_measurement" && x.QualityDecision == "included" && x.WaterLevelM != nil {
				points = append(points, *x.WaterLevelM)
			}
		}
		if len(points) == 0 {
			return fmt.Errorf("%w: 无冻结评级测次", domain.ErrGate)
		}
		sort.Float64s(points)
		lower, upper := in.RequestedLowerBoundM, in.RequestedUpperBoundM
		if lower == 0 && upper == 0 {
			lower = points[0]
			upper = math.Max(points[len(points)-1], in.HistoricalHighM)
		}
		req := assessment.BoundaryRequest{RequestedLowerBoundM: lower, RequestedUpperBoundM: upper, MaxLowExtensionRatio: lowRatio, MaxHighExtensionRatio: highRatio, HistoricalHighM: in.HistoricalHighM}
		run, deviations, err := s.engine.AssessBounded(c, in.RunID, in.ActorID, req, s.now())
		if err != nil {
			return err
		}
		c.Assessment = run
		if err := domain.ValidateAssessment(*run); err != nil {
			return err
		}
		for i := range deviations {
			domain.InitializeDeviation(&deviations[i], in.ActorID, s.now())
			deviations[i].PhaseHistory[0].Description = "评估门禁自动建立"
		}
		c.Deviations = append(c.Deviations, deviations...)
		return c.Advance(domain.Assessed)
	})
}
func (s *Service) AddDeviation(ctx context.Context, id string, in DeviationInput) (Result, error) {
	return s.mutateCommand(ctx, id, in.Meta, "deviation_opened", "deviation_opened/"+in.DeviationID, func(c *domain.Case, _ *repository.Tx) error {
		if err := c.RequireState(domain.Assessed); err != nil {
			return err
		}
		if in.DeviationID == "" || in.SourceGate == "" {
			return fmt.Errorf("%w: deviation_id 和 source_gate 必填", domain.ErrInvalid)
		}
		if in.Severity != "minor" && in.Severity != "major" && in.Severity != "critical" {
			return fmt.Errorf("%w: severity 无效", domain.ErrInvalid)
		}
		deviation := domain.Deviation{DeviationID: in.DeviationID, SourceGate: in.SourceGate, Severity: in.Severity, State: "open"}
		domain.InitializeDeviation(&deviation, in.ActorID, s.now())
		if err := domain.ValidateDeviation(deviation); err != nil {
			return err
		}
		c.Deviations = append(c.Deviations, deviation)
		return nil
	})
}
func (s *Service) Remediate(ctx context.Context, caseID, deviationID string, in RemediationInput) (Result, error) {
	return s.mutateCommand(ctx, caseID, in.Meta, "deviation_verified", "deviation_verified/"+deviationID, func(c *domain.Case, _ *repository.Tx) error {
		if err := c.RequireState(domain.Assessed); err != nil {
			return err
		}
		if !in.RetestPassed {
			return fmt.Errorf("%w: 定向复验未通过", domain.ErrGate)
		}
		d, err := deviation(c, deviationID)
		if err != nil {
			return err
		}
		if s.now().After(d.DueAt) {
			d.EverOverdue = true
			if len(d.DueDateRevisions) == 0 {
				return fmt.Errorf("%w: overdue_due_date_revision_required", domain.ErrGate)
			}
		}
		if in.VerifiedBy == "" || in.VerifiedBy == in.ActorID || in.VerifiedBy == d.CorrectedBy {
			return fmt.Errorf("%w: 复验人必须独立于整改人", domain.ErrGate)
		}
		d.CorrectedBy = in.ActorID
		if err := c.CloseDeviation(deviationID, in.Containment, in.RootCause, in.CorrectiveAction, in.VerificationEvidenceRef, in.VerifiedBy, s.now()); err != nil {
			return err
		}
		for i := range c.Deviations {
			if c.Deviations[i].DeviationID == deviationID {
				c.Deviations[i].RetestVerdict = "pass"
			}
		}
		if c.AllDeviationsClosed() {
			return c.Advance(domain.DeviationsClosed)
		}
		return nil
	})
}
func (s *Service) CloseNoDeviations(ctx context.Context, id string, m Meta) (Result, error) {
	return s.mutate(ctx, id, m, "deviations_closed", func(c *domain.Case, _ *repository.Tx) error {
		if err := c.RequireState(domain.Assessed); err != nil {
			return err
		}
		if !c.AllDeviationsClosed() {
			return fmt.Errorf("%w: 尚有未关闭偏差", domain.ErrGate)
		}
		return c.Advance(domain.DeviationsClosed)
	})
}
func (s *Service) Review(ctx context.Context, id string, in ReviewInput) (Result, error) {
	return s.mutate(ctx, id, in.Meta, "independent_review_signed", func(c *domain.Case, tx *repository.Tx) error {
		if err := c.RequireState(domain.DeviationsClosed); err != nil {
			return err
		}
		if in.ReviewerID == "" || in.ReviewerID != in.ActorID || in.ReviewerID == c.OwnerID || in.ReviewerID == c.ModelerID {
			return fmt.Errorf("%w: 复核员必须与责任人及建模人隔离", domain.ErrGate)
		}
		events, err := tx.Events(ctx, id)
		if err != nil {
			return err
		}
		if ok, pos, msg := audit.Verify(events); !ok {
			return fmt.Errorf("%w: audit_chain position=%d message=%s", domain.ErrGate, pos, msg)
		}
		preflight := buildReviewPreflight(c, events, in.ReviewerID)
		if !preflight.Eligible {
			return fmt.Errorf("%w: reviewer_duty_conflict", domain.ErrGate)
		}
		if in.MaterialsDigest == "" || in.MaterialsDigest != preflight.MaterialsDigest {
			return fmt.Errorf("%w: review materials_digest 已陈旧", domain.ErrConflict)
		}
		if in.Decision != "pass" && in.Decision != "return" {
			return fmt.Errorf("%w: review decision 无效", domain.ErrInvalid)
		}
		if in.Decision == "return" && len(in.Issues) == 0 {
			return fmt.Errorf("%w: 退回决定至少包含一个问题项", domain.ErrInvalid)
		}
		if len(c.ReviewRounds) > 0 {
			pending := c.ReviewRounds[len(c.ReviewRounds)-1]
			if pending.Decision == "pending" && pending.ReviewerID != in.ReviewerID {
				return fmt.Errorf("%w: 复核员与重提轮次不一致", domain.ErrGate)
			}
		}
		seen := map[string]bool{}
		for i := range in.Issues {
			q := &in.Issues[i]
			if q.IssueID == "" || q.Category == "" || q.Description == "" || q.RequiredAction == "" {
				return fmt.Errorf("%w: 复核问题项字段不完整", domain.ErrInvalid)
			}
			if seen[q.IssueID] {
				return fmt.Errorf("%w: issue_id 重复", domain.ErrInvalid)
			}
			seen[q.IssueID] = true
		}
		statement := "复核员未参与案件创建、关键证据裁定、建模、整改或偏差验证"
		c.Review = &domain.Review{ReviewerID: in.ReviewerID, Decision: in.Decision, Comment: in.Comment, SignedAt: s.now(), MaterialsDigest: in.MaterialsDigest, IndependenceStatement: statement}
		round := 1
		if len(c.ReviewRounds) > 0 {
			round = c.ReviewRounds[len(c.ReviewRounds)-1].Round
			if c.ReviewRounds[len(c.ReviewRounds)-1].Decision == "pending" {
				c.ReviewRounds = c.ReviewRounds[:len(c.ReviewRounds)-1]
			} else {
				round++
			}
		}
		at := s.now()
		c.ReviewRounds = append(c.ReviewRounds, domain.ReviewRound{Round: round, ReviewerID: in.ReviewerID, Decision: in.Decision, Comment: in.Comment, SignedAt: &at, Issues: in.Issues, Frozen: in.Decision == "pass", MaterialsDigest: in.MaterialsDigest, IndependenceStatement: statement})
		if in.Decision == "return" {
			return nil
		}
		return c.Advance(domain.Reviewed)
	})
}
func (s *Service) IssueTrial(ctx context.Context, id string, in TrialInput) (Result, error) {
	return s.mutateCommand(ctx, id, in.Meta, "trial_issued", "trial_issued/"+in.DecisionID, func(c *domain.Case, tx *repository.Tx) error {
		if err := c.RequireState(domain.Reviewed); err != nil {
			return err
		}
		if c.Assessment == nil {
			return fmt.Errorf("%w: 缺少评估", domain.ErrGate)
		}
		if len(c.ReviewRounds) == 0 || c.ReviewRounds[len(c.ReviewRounds)-1].Decision != "pass" || c.ReviewRounds[len(c.ReviewRounds)-1].Invalidated {
			return fmt.Errorf("%w: 最新复核轮次未通过", domain.ErrGate)
		}
		if in.EffectiveUntil.Before(in.EffectiveFrom) || in.EffectiveFrom.IsZero() || in.RollbackCondition == "" {
			return fmt.Errorf("%w: 试用时效或回退条件无效", domain.ErrInvalid)
		}
		d := domain.Decision{DecisionID: in.DecisionID, DecisionType: "trial", CurveVersion: c.CandidateVersion, AuthorizedBy: in.AuthorizedBy, EffectiveFrom: in.EffectiveFrom, EffectiveUntil: &in.EffectiveUntil, LowerBoundM: c.Assessment.LowerBoundM, UpperBoundM: c.Assessment.UpperBoundM, RollbackCondition: in.RollbackCondition, Status: "active"}
		if err := domain.ValidateDecision(d); err != nil {
			return err
		}
		if err := tx.RegisterTrial(ctx, c, d); err != nil {
			return err
		}
		c.Decisions = append(c.Decisions, d)
		return c.Advance(domain.TrialActive)
	})
}
func (s *Service) ObserveTrial(ctx context.Context, id string, in ObservationInput) (Result, error) {
	return s.mutateCommand(ctx, id, in.Meta, "trial_observation_registered", "trial_observation_registered/"+in.ObservationID, func(c *domain.Case, _ *repository.Tx) error {
		if err := c.RequireState(domain.TrialActive); err != nil {
			return err
		}
		if c.Assessment == nil || in.WaterLevelM < c.Assessment.LowerBoundM || in.WaterLevelM > c.Assessment.UpperBoundM {
			return fmt.Errorf("%w: 校核水位超出适用边界", domain.ErrGate)
		}
		for _, existing := range c.TrialObservations {
			if existing.ObservationID == in.ObservationID {
				return fmt.Errorf("%w: observation_id 重复", domain.ErrConflict)
			}
			if existing.ObservedAt.Equal(in.ObservedAt) && math.Abs(existing.WaterLevelM-in.WaterLevelM) < 1e-9 {
				return fmt.Errorf("%w: observed_at 与 water_level_m 组合重复", domain.ErrConflict)
			}
		}
		for _, actor := range trialRestrictedActors(c) {
			if in.ActorID == actor {
				return fmt.Errorf("%w: trial_observer_duty_conflict", domain.ErrGate)
			}
		}
		var trial *domain.Decision
		for i := range c.Decisions {
			if c.Decisions[i].DecisionType == "trial" {
				trial = &c.Decisions[i]
			}
		}
		if trial == nil || in.ObservedAt.Before(trial.EffectiveFrom) || trial.EffectiveUntil == nil || in.ObservedAt.After(*trial.EffectiveUntil) {
			return fmt.Errorf("%w: 校核测次不在试用有效期内", domain.ErrGate)
		}
		o, err := s.engine.TrialObservation(in.ObservationID, in.ObservedAt, in.WaterLevelM, in.MeasuredDischargeM3S, in.PredictedDischargeM3S)
		if err != nil {
			return err
		}
		if err := domain.ValidateTrialObservation(o); err != nil {
			return err
		}
		o.SubmittedBy = in.ActorID
		o.TrialDecisionID = trial.DecisionID
		o.RecordState = "active"
		o.Band = assessment.TrialBand(c.Assessment, o.WaterLevelM)
		o.CountsTowardProgress = true
		c.TrialObservations = append(c.TrialObservations, o)
		if o.Verdict == "suspend" {
			sid := "suspension-" + in.ObservationID
			o.SuspensionID = sid
			c.TrialObservations[len(c.TrialObservations)-1] = o
			c.TrialSuspensions = append(c.TrialSuspensions, domain.TrialSuspension{SuspensionID: sid, TriggerObservationID: o.ObservationID, ActualBias: math.Abs(o.RelativeBias), Threshold: s.engine.TrialBiasLimit, SuspendedAt: s.now(), RollbackRequired: true, State: "active", ObservationActorID: in.ActorID, RecoveryAttempts: []domain.RecoveryAttempt{}})
			c.State = domain.TrialSuspended
			return nil
		}
		refreshTrialContributions(c)
		return nil
	})
}
func (s *Service) Activate(ctx context.Context, id string, in ActivationInput) (Result, error) {
	return s.mutateCommand(ctx, id, in.Meta, "curve_activated", "curve_activated/"+in.DecisionID, func(c *domain.Case, tx *repository.Tx) error {
		if err := c.RequireState(domain.TrialQualified); err != nil {
			return err
		}
		if in.AuthorizedBy == "" || in.RollbackCondition == "" {
			return fmt.Errorf("%w: 授权人和回退条件必填", domain.ErrInvalid)
		}
		if in.CurrentVersionDigest == "" {
			return fmt.Errorf("%w: current_version_digest 必填", domain.ErrInvalid)
		}
		for _, x := range c.TrialSuspensions {
			if x.State == "active" {
				return fmt.Errorf("%w: trial_suspension_active", domain.ErrGate)
			}
		}
		d := domain.Decision{DecisionID: in.DecisionID, DecisionType: "activation", CurveVersion: c.CandidateVersion, AuthorizedBy: in.AuthorizedBy, EffectiveFrom: domain.NormalizedTime(in.EffectiveFrom), LowerBoundM: c.Assessment.LowerBoundM, UpperBoundM: c.Assessment.UpperBoundM, RollbackCondition: in.RollbackCondition}
		if err := domain.ValidateDecision(d); err != nil {
			return err
		}
		c.Decisions = append(c.Decisions, d)
		if err := tx.Activate(ctx, c, d, in.CurrentVersionDigest); err != nil {
			return err
		}
		return c.Advance(domain.Activated)
	})
}

func (s *Service) Archive(ctx context.Context, id string, m Meta) (Result, error) {
	if err := validateMeta(m); err != nil {
		return Result{}, err
	}
	stationID, err := s.store.CaseStation(ctx, id)
	if err != nil {
		return Result{}, err
	}
	lock := s.stationLock(stationID)
	lock.Lock()
	defer lock.Unlock()
	var out Result
	err = s.store.Within(ctx, func(tx *repository.Tx) error {
		storedCaseID, command, status, body, ok, err := tx.GetIdempotent(ctx, m.RequestID)
		if err != nil {
			return err
		}
		if ok {
			if storedCaseID != id || (command != "" && command != "case_archived") {
				return fmt.Errorf("%w: request_id 已用于不同命令", domain.ErrConflict)
			}
			out = Result{Status: status, Body: body, Replayed: true}
			return nil
		}
		c, err := tx.LoadCase(ctx, id)
		if err != nil {
			return err
		}
		if c.Revision != m.ExpectedRevision {
			return domain.ErrConflict
		}
		if err = c.RequireState(domain.Activated); err != nil {
			return err
		}
		previous := c.Revision
		c.State = domain.Archived
		c.Revision++
		at := s.now()
		c.ArchivedAt = &at
		if err := c.ValidateConsistency(); err != nil {
			return err
		}
		projection, err := tx.ArchiveProjection(ctx, c)
		if err != nil {
			return err
		}
		expectedAssessments := 0
		if c.Assessment != nil {
			expectedAssessments = 1
		}
		checks := []struct {
			name             string
			actual, expected int
		}{{"evidence", projection.Evidence, len(c.Evidence)}, {"instrument_qualifications", projection.Qualifications, len(c.Qualifications)}, {"assessment_runs", projection.Assessments, expectedAssessments}, {"deviations", projection.Deviations, len(c.Deviations)}, {"activation_decisions", projection.Decisions, len(c.Decisions)}, {"trial_observations", projection.Observations, len(c.TrialObservations)}}
		for _, x := range checks {
			if x.actual != x.expected {
				return fmt.Errorf("%w: archive_projection_mismatch kind=%s expected=%d actual=%d", domain.ErrGate, x.name, x.expected, x.actual)
			}
		}
		if projection.CurveCaseID != c.CaseID || projection.CurveVersion != c.CandidateVersion {
			return fmt.Errorf("%w: archive_curve_pointer_mismatch", domain.ErrGate)
		}
		events, err := tx.Events(ctx, id)
		if err != nil {
			return err
		}
		if ok, pos, msg := audit.Verify(events); !ok {
			return fmt.Errorf("%w: audit_chain position=%d message=%s", domain.ErrGate, pos, msg)
		}
		prev := ""
		if len(events) > 0 {
			prev = events[len(events)-1].EventDigest
		}
		event, err := audit.BuildEvent(id, int64(len(events)+1), "case_archived", m.ActorID, m.RequestID, at, c, prev)
		if err != nil {
			return err
		}
		events = append(events, event)
		if err = tx.AppendEvent(ctx, event); err != nil {
			return err
		}
		if err = tx.SaveCase(ctx, c, previous); err != nil {
			return err
		}
		archive, err := audit.BuildArchive(*c, events, at)
		if err != nil {
			return err
		}
		if ok, msg := audit.VerifyArchive(archive); !ok {
			return fmt.Errorf("%w: archive_integrity %s", domain.ErrGate, msg)
		}
		if err = tx.SaveArchive(ctx, archive); err != nil {
			return err
		}
		body = marshal(struct {
			Case          *domain.Case `json:"case"`
			ArchiveDigest string       `json:"archive_digest"`
		}{c, archive.Digest})
		if err = tx.SaveIdempotent(ctx, m.RequestID, id, "case_archived", 200, body); err != nil {
			return err
		}
		out = Result{Status: 200, Body: body}
		return nil
	})
	return out, err
}

func (s *Service) Get(ctx context.Context, id string) (*domain.Case, error) {
	return s.store.LoadCase(ctx, id)
}
func (s *Service) Timeline(ctx context.Context, id string) ([]domain.AuditEvent, error) {
	if _, err := s.store.LoadCase(ctx, id); err != nil {
		return nil, err
	}
	return s.store.Events(ctx, id)
}
func (s *Service) GetArchive(ctx context.Context, id string) (any, error) {
	a, err := s.store.Archive(ctx, id)
	if err != nil {
		return nil, err
	}
	ok, msg := audit.VerifyArchive(a)
	return struct {
		Archive          audit.Archive `json:"archive"`
		IntegrityOK      bool          `json:"integrity_ok"`
		IntegrityMessage string        `json:"integrity_message"`
	}{a, ok, msg}, nil
}
func (s *Service) CurrentCurve(ctx context.Context, station string) (map[string]string, error) {
	return s.store.CurrentCurve(ctx, station)
}
func ErrorKind(err error) string {
	for _, pair := range []struct {
		target error
		kind   string
	}{{domain.ErrNotFound, "not_found"}, {domain.ErrConflict, "revision_conflict"}, {domain.ErrArchived, "case_archived"}, {domain.ErrInvalid, "validation_failed"}, {domain.ErrGate, "gate_failed"}} {
		if errors.Is(err, pair.target) {
			return pair.kind
		}
	}
	return "internal_error"
}
