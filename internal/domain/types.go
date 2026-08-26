package domain

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"
)

type State string

const (
	Draft             State = "draft"
	BaselineFrozen    State = "baseline_frozen"
	EvidenceQualified State = "evidence_qualified"
	Assessed          State = "assessed"
	DeviationsClosed  State = "deviations_closed"
	Reviewed          State = "reviewed"
	TrialActive       State = "trial_active"
	TrialSuspended    State = "trial_suspended"
	TrialQualified    State = "trial_qualified"
	Activated         State = "activated"
	Archived          State = "archived"
)

var (
	ErrNotFound = errors.New("resource_not_found")
	ErrConflict = errors.New("revision_conflict")
	ErrArchived = errors.New("case_archived")
	ErrInvalid  = errors.New("validation_failed")
	ErrGate     = errors.New("gate_failed")
)

type Evidence struct {
	EvidenceID           string              `json:"evidence_id"`
	EvidenceType         string              `json:"evidence_type"`
	ObservedAt           time.Time           `json:"observed_at"`
	WaterLevelM          *float64            `json:"water_level_m,omitempty"`
	DischargeM3S         *float64            `json:"discharge_m3s,omitempty"`
	SourceRef            string              `json:"source_ref"`
	ContentDigest        string              `json:"content_digest"`
	QualityDecision      string              `json:"quality_decision"`
	DecisionReason       string              `json:"decision_reason"`
	Version              int                 `json:"version"`
	PreviousDigest       string              `json:"previous_digest,omitempty"`
	CorrectionReason     string              `json:"correction_reason,omitempty"`
	CorrectedBy          string              `json:"corrected_by,omitempty"`
	CorrectedAt          *time.Time          `json:"corrected_at,omitempty"`
	FloodEventID         string              `json:"flood_event_id,omitempty"`
	VerticalUncertaintyM *float64            `json:"vertical_uncertainty_m,omitempty"`
	DatumID              string              `json:"datum_id,omitempty"`
	ConfidenceLevel      string              `json:"confidence_level,omitempty"`
	InstrumentBindings   []InstrumentBinding `json:"instrument_bindings"`
}

type Qualification struct {
	QualificationID  string     `json:"qualification_id"`
	InstrumentID     string     `json:"instrument_id"`
	InstrumentKind   string     `json:"instrument_kind"`
	CertificateRef   string     `json:"certificate_ref"`
	CalibratedAt     time.Time  `json:"calibrated_at"`
	ValidUntil       time.Time  `json:"valid_until"`
	UsageStartedAt   time.Time  `json:"usage_started_at"`
	UsageEndedAt     time.Time  `json:"usage_ended_at"`
	Verdict          string     `json:"verdict"`
	Version          int        `json:"version"`
	Digest           string     `json:"digest"`
	PreviousDigest   string     `json:"previous_digest,omitempty"`
	CorrectionReason string     `json:"correction_reason,omitempty"`
	CorrectedBy      string     `json:"corrected_by,omitempty"`
	CorrectedAt      *time.Time `json:"corrected_at,omitempty"`
}

type Assessment struct {
	RunID                 string                `json:"run_id"`
	InputDigest           string                `json:"input_digest"`
	MethodVersion         string                `json:"method_version"`
	Parameters            map[string]float64    `json:"parameters"`
	ResidualMetrics       map[string]float64    `json:"residual_metrics"`
	LowerBoundM           float64               `json:"lower_bound_m"`
	UpperBoundM           float64               `json:"upper_bound_m"`
	ExtrapolationRatio    float64               `json:"extrapolation_ratio"`
	Verdict               string                `json:"verdict"`
	CompletedAt           time.Time             `json:"completed_at"`
	ResidualDetails       []ResidualDetail      `json:"residual_details"`
	BandSummaries         []ResidualBandSummary `json:"band_summaries"`
	InfluenceDetails      []InfluenceDetail     `json:"influence_details"`
	FloodMarkConstraints  []FloodMarkConstraint `json:"flood_mark_constraints"`
	RequestedLowerBoundM  float64               `json:"requested_lower_bound_m"`
	RequestedUpperBoundM  float64               `json:"requested_upper_bound_m"`
	MaxLowExtensionRatio  float64               `json:"max_low_extension_ratio"`
	MaxHighExtensionRatio float64               `json:"max_high_extension_ratio"`
	BoundaryDiagnostics   []BoundaryDiagnostic  `json:"boundary_diagnostics"`
}

type Deviation struct {
	DeviationID             string              `json:"deviation_id"`
	SourceGate              string              `json:"source_gate"`
	Severity                string              `json:"severity"`
	State                   string              `json:"state"`
	Containment             string              `json:"containment,omitempty"`
	RootCause               string              `json:"root_cause,omitempty"`
	CorrectiveAction        string              `json:"corrective_action,omitempty"`
	VerificationEvidenceRef string              `json:"verification_evidence_ref,omitempty"`
	VerifiedBy              string              `json:"verified_by,omitempty"`
	VerifiedAt              *time.Time          `json:"verified_at,omitempty"`
	RetestVerdict           string              `json:"retest_verdict,omitempty"`
	CreatedBy               string              `json:"created_by,omitempty"`
	CorrectedBy             string              `json:"corrected_by,omitempty"`
	PhaseHistory            []DeviationPhase    `json:"phase_history"`
	Retests                 []DeviationRetest   `json:"retests"`
	CreatedAt               time.Time           `json:"created_at"`
	OriginalDueAt           time.Time           `json:"original_due_at"`
	DueAt                   time.Time           `json:"due_at"`
	DueDateRevisions        []DueDateRevision   `json:"due_date_revisions"`
	EverOverdue             bool                `json:"ever_overdue"`
	CorrectionAttempts      []CorrectionAttempt `json:"correction_attempts"`
}

type Decision struct {
	DecisionID         string     `json:"decision_id"`
	DecisionType       string     `json:"decision_type"`
	CurveVersion       string     `json:"curve_version"`
	AuthorizedBy       string     `json:"authorized_by"`
	EffectiveFrom      time.Time  `json:"effective_from"`
	EffectiveUntil     *time.Time `json:"effective_until,omitempty"`
	LowerBoundM        float64    `json:"lower_bound_m"`
	UpperBoundM        float64    `json:"upper_bound_m"`
	RollbackCondition  string     `json:"rollback_condition"`
	ReplacesVersion    string     `json:"replaces_version,omitempty"`
	Status             string     `json:"status,omitempty"`
	InvalidatedAt      *time.Time `json:"invalidated_at,omitempty"`
	InvalidationReason string     `json:"invalidation_reason,omitempty"`
}

type TrialObservation struct {
	ObservationID          string    `json:"observation_id"`
	ObservedAt             time.Time `json:"observed_at"`
	WaterLevelM            float64   `json:"water_level_m"`
	MeasuredDischargeM3S   float64   `json:"measured_discharge_m3s"`
	PredictedDischargeM3S  float64   `json:"predicted_discharge_m3s"`
	RelativeBias           float64   `json:"relative_bias"`
	Verdict                string    `json:"verdict"`
	SubmittedBy            string    `json:"submitted_by,omitempty"`
	SuspensionID           string    `json:"suspension_id,omitempty"`
	Band                   string    `json:"band,omitempty"`
	CountsTowardProgress   bool      `json:"counts_toward_progress"`
	RecoveryConfirmation   bool      `json:"recovery_confirmation"`
	RecordState            string    `json:"record_state"`
	SupersededBy           string    `json:"superseded_by,omitempty"`
	Supersedes             string    `json:"supersedes,omitempty"`
	SupersededReason       string    `json:"superseded_reason,omitempty"`
	ReplacementEvidenceRef string    `json:"replacement_evidence_ref,omitempty"`
	TrialDecisionID        string    `json:"trial_decision_id,omitempty"`
}

type Review struct {
	ReviewerID            string    `json:"reviewer_id"`
	Decision              string    `json:"decision"`
	Comment               string    `json:"comment"`
	SignedAt              time.Time `json:"signed_at"`
	MaterialsDigest       string    `json:"materials_digest"`
	IndependenceStatement string    `json:"independence_statement"`
}

type Case struct {
	CaseID                  string                   `json:"case_id"`
	StationID               string                   `json:"station_id"`
	RiverReach              string                   `json:"river_reach"`
	CandidateVersion        string                   `json:"candidate_version"`
	OwnerID                 string                   `json:"owner_id"`
	ModelerID               string                   `json:"modeler_id,omitempty"`
	State                   State                    `json:"state"`
	Revision                int64                    `json:"revision"`
	BaselineDigest          string                   `json:"baseline_digest,omitempty"`
	CreatedAt               time.Time                `json:"created_at"`
	ArchivedAt              *time.Time               `json:"archived_at,omitempty"`
	Evidence                []Evidence               `json:"evidence"`
	Qualifications          []Qualification          `json:"instrument_qualifications"`
	Assessment              *Assessment              `json:"assessment,omitempty"`
	Deviations              []Deviation              `json:"deviations"`
	Review                  *Review                  `json:"review,omitempty"`
	Decisions               []Decision               `json:"activation_decisions"`
	TrialObservations       []TrialObservation       `json:"trial_observations"`
	EvidenceVersions        []EvidenceVersion        `json:"evidence_versions"`
	ReviewRounds            []ReviewRound            `json:"review_rounds"`
	TrialSuspensions        []TrialSuspension        `json:"trial_suspensions"`
	QualityDecisionHistory  []QualityDecisionRecord  `json:"quality_decision_history"`
	QualificationVersions   []QualificationVersion   `json:"qualification_versions"`
	BaselineManifest        *BaselineManifest        `json:"baseline_manifest,omitempty"`
	InstrumentInvalidations []InstrumentInvalidation `json:"instrument_invalidations"`
	TrialExpirySettlements  []TrialExpirySettlement  `json:"trial_expiry_settlements"`
}

type AuditEvent struct {
	EventID        string          `json:"event_id"`
	CaseID         string          `json:"case_id"`
	SequenceNo     int64           `json:"sequence_no"`
	EventType      string          `json:"event_type"`
	ActorID        string          `json:"actor_id"`
	RequestID      string          `json:"request_id"`
	OccurredAt     time.Time       `json:"occurred_at"`
	Payload        json.RawMessage `json:"payload"`
	PreviousDigest string          `json:"previous_digest"`
	EventDigest    string          `json:"event_digest"`
}

func ValidateCreate(c *Case) error {
	if strings.TrimSpace(c.CaseID) == "" || strings.TrimSpace(c.StationID) == "" || strings.TrimSpace(c.RiverReach) == "" || strings.TrimSpace(c.CandidateVersion) == "" || strings.TrimSpace(c.OwnerID) == "" {
		return fmt.Errorf("%w: case_id、station_id、river_reach、candidate_version 和 owner_id 均为必填", ErrInvalid)
	}
	if len(c.StationID) > 80 || len(c.CandidateVersion) > 80 || len(c.OwnerID) > 80 {
		return fmt.Errorf("%w: 标识长度超限", ErrInvalid)
	}
	return nil
}

func (c *Case) EnsureMutable() error {
	if c.State == Archived {
		return ErrArchived
	}
	return nil
}

func (c *Case) RequireState(states ...State) error {
	for _, state := range states {
		if c.State == state {
			return nil
		}
	}
	return fmt.Errorf("%w: 当前状态 %s 不允许此操作", ErrGate, c.State)
}

func (c *Case) Advance(next State) error {
	allowed := map[State]State{Draft: BaselineFrozen, BaselineFrozen: EvidenceQualified, EvidenceQualified: Assessed, Assessed: DeviationsClosed, DeviationsClosed: Reviewed, Reviewed: TrialActive, TrialActive: TrialQualified, TrialQualified: Activated, Activated: Archived}
	if allowed[c.State] != next {
		return fmt.Errorf("%w: 非法状态转换 %s -> %s", ErrGate, c.State, next)
	}
	c.State = next
	return nil
}

func (c *Case) FreezeBaseline() error {
	if err := c.RequireState(Draft); err != nil {
		return err
	}
	counts := map[string]int{}
	for _, e := range c.Evidence {
		counts[e.EvidenceType]++
	}
	for _, typ := range []string{"rating_measurement", "cross_section", "field_record"} {
		if counts[typ] == 0 {
			return fmt.Errorf("%w: 缺少必备证据 %s", ErrGate, typ)
		}
	}
	manifest := BuildBaselineManifest(c, time.Now().UTC())
	c.BaselineManifest = &manifest
	c.BaselineDigest = manifest.Digest
	c.MarkBaselineVersions()
	return c.Advance(BaselineFrozen)
}

func ComputeBaselineDigest(c *Case) string {
	sortedEvidence := append([]Evidence(nil), c.Evidence...)
	sort.Slice(sortedEvidence, func(i, j int) bool { return sortedEvidence[i].EvidenceID < sortedEvidence[j].EvidenceID })
	sortedQualifications := append([]Qualification(nil), c.Qualifications...)
	sort.Slice(sortedQualifications, func(i, j int) bool {
		return sortedQualifications[i].QualificationID < sortedQualifications[j].QualificationID
	})
	b, _ := json.Marshal(struct {
		Evidence       []Evidence      `json:"evidence"`
		Qualifications []Qualification `json:"instrument_qualifications"`
	}{sortedEvidence, sortedQualifications})
	h := sha256.Sum256(b)
	return hex.EncodeToString(h[:])
}

func (c *Case) QualifyEvidence() error {
	if err := c.RequireState(BaselineFrozen); err != nil {
		return err
	}
	measurementCount := 0
	measurementKeys := map[string]string{}
	evidence, err := c.FrozenEvidence()
	if err != nil {
		return err
	}
	qualifications, err := c.FrozenQualifications()
	if err != nil {
		return err
	}
	for _, e := range evidence {
		if e.QualityDecision == "pending" || e.QualityDecision == "pending_explanation" || e.QualityDecision == "" {
			return fmt.Errorf("%w: 证据 %s 尚未裁定", ErrGate, e.EvidenceID)
		}
		if e.QualityDecision == "excluded" && strings.TrimSpace(e.DecisionReason) == "" {
			return fmt.Errorf("%w: 排除测次必须说明原因", ErrGate)
		}
		if e.ObservedAt.IsZero() {
			return fmt.Errorf("%w: 证据 %s 缺少观测时刻", ErrGate, e.EvidenceID)
		}
		if e.EvidenceType == "rating_measurement" && e.QualityDecision == "included" {
			if e.WaterLevelM == nil || e.DischargeM3S == nil || *e.WaterLevelM < -20 || *e.WaterLevelM > 100 || *e.DischargeM3S <= 0 {
				return fmt.Errorf("%w: 测次 %s 数值范围无效", ErrGate, e.EvidenceID)
			}
			key := fmt.Sprintf("%s/%.6f/%.6f", e.ObservedAt.UTC().Format(time.RFC3339Nano), *e.WaterLevelM, *e.DischargeM3S)
			if previous := measurementKeys[key]; previous != "" {
				return fmt.Errorf("%w: 测次 %s 与 %s 重复，必须显式排除一项", ErrGate, e.EvidenceID, previous)
			}
			measurementKeys[key] = e.EvidenceID
			measurementCount++
		}
	}
	if measurementCount < 3 {
		return fmt.Errorf("%w: 至少需要 3 个合格评级测次", ErrGate)
	}
	kinds := map[string]bool{}
	for _, q := range qualifications {
		if q.Verdict == "qualified" {
			kinds[q.InstrumentKind] = true
		}
	}
	for _, kind := range []string{"current_meter", "water_level_gauge", "survey_equipment"} {
		if !kinds[kind] {
			return fmt.Errorf("%w: 缺少合格仪器 %s", ErrGate, kind)
		}
	}
	for _, e := range evidence {
		if err := ValidateBindings(e, qualifications, true); err != nil {
			return err
		}
	}
	return c.Advance(EvidenceQualified)
}

func (c *Case) CloseDeviation(id, containment, root, corrective, ref, verifier string, at time.Time) error {
	if verifier == "" || verifier == c.OwnerID || verifier == c.ModelerID {
		return fmt.Errorf("%w: 验证人必须职责隔离", ErrGate)
	}
	for i := range c.Deviations {
		if c.Deviations[i].DeviationID == id {
			if containment == "" || root == "" || corrective == "" || ref == "" {
				return fmt.Errorf("%w: 偏差闭环信息不完整", ErrInvalid)
			}
			c.Deviations[i].Containment, c.Deviations[i].RootCause = containment, root
			c.Deviations[i].CorrectiveAction, c.Deviations[i].VerificationEvidenceRef = corrective, ref
			c.Deviations[i].VerifiedBy, c.Deviations[i].VerifiedAt, c.Deviations[i].State = verifier, &at, "verified"
			return nil
		}
	}
	return ErrNotFound
}

func (c *Case) AllDeviationsClosed() bool {
	for _, d := range c.Deviations {
		if d.State != "verified" {
			return false
		}
	}
	return true
}

func Digest(v any) string {
	b, _ := json.Marshal(v)
	h := sha256.Sum256(b)
	return hex.EncodeToString(h[:])
}
