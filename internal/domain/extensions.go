package domain

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"sort"
	"strings"
	"time"
)

func ContentDigest(content string) string {
	sum := sha256.Sum256([]byte(content))
	return hex.EncodeToString(sum[:])
}

type EvidenceVersion struct {
	EvidenceID              string    `json:"evidence_id"`
	Version                 int       `json:"version"`
	ContentDigest           string    `json:"content_digest"`
	PreviousDigest          string    `json:"previous_digest,omitempty"`
	CorrectionReason        string    `json:"correction_reason,omitempty"`
	ActorID                 string    `json:"actor_id"`
	CreatedAt               time.Time `json:"created_at"`
	IncludedInBaseline      bool      `json:"included_in_baseline"`
	BaselineQualityDecision string    `json:"baseline_quality_decision,omitempty"`
	Snapshot                Evidence  `json:"snapshot"`
}

type QualityDecisionChange struct {
	EvidenceID string `json:"evidence_id"`
	Decision   string `json:"decision"`
	Reason     string `json:"reason"`
}

type QualityDecisionRecord struct {
	EvidenceID  string    `json:"evidence_id"`
	OldDecision string    `json:"old_decision"`
	NewDecision string    `json:"new_decision"`
	Reason      string    `json:"reason"`
	ActorID     string    `json:"actor_id"`
	OccurredAt  time.Time `json:"occurred_at"`
}

type ResidualDetail struct {
	EvidenceID       string  `json:"evidence_id"`
	WaterLevelM      float64 `json:"water_level_m"`
	MeasuredM3S      float64 `json:"measured_discharge_m3s"`
	PredictedM3S     float64 `json:"predicted_discharge_m3s"`
	AbsoluteResidual float64 `json:"absolute_residual"`
	RelativeResidual float64 `json:"relative_residual"`
	Band             string  `json:"band"`
	Threshold        float64 `json:"threshold"`
	Verdict          string  `json:"verdict"`
}

type MetricCheck struct {
	Actual    float64 `json:"actual"`
	Threshold float64 `json:"threshold"`
	Verdict   string  `json:"verdict"`
}

type ResidualBandSummary struct {
	Band        string      `json:"band"`
	SampleCount int         `json:"sample_count"`
	SignedBias  MetricCheck `json:"mean_signed_bias"`
	RMSE        MetricCheck `json:"root_mean_square_error"`
	MaximumAbs  MetricCheck `json:"maximum_absolute_error"`
	Verdict     string      `json:"verdict"`
}

type DeviationPhase struct {
	State       string    `json:"state"`
	ActorID     string    `json:"actor_id"`
	OccurredAt  time.Time `json:"occurred_at"`
	Description string    `json:"description"`
	EvidenceRef string    `json:"evidence_ref,omitempty"`
}

type DeviationRetest struct {
	RetestID      string    `json:"retest_id"`
	Rule          string    `json:"rule"`
	Actual        float64   `json:"actual"`
	Threshold     float64   `json:"threshold"`
	Verdict       string    `json:"verdict"`
	VerifiedBy    string    `json:"verified_by"`
	VerifiedAt    time.Time `json:"verified_at"`
	FailureReason string    `json:"failure_reason,omitempty"`
}

type ReviewIssue struct {
	IssueID        string     `json:"issue_id"`
	Category       string     `json:"category"`
	RelatedID      string     `json:"related_id,omitempty"`
	Description    string     `json:"description"`
	RequiredAction string     `json:"required_action"`
	Response       string     `json:"response,omitempty"`
	EvidenceRef    string     `json:"evidence_ref,omitempty"`
	RespondedBy    string     `json:"responded_by,omitempty"`
	RespondedAt    *time.Time `json:"responded_at,omitempty"`
}

type ReviewRound struct {
	Round                 int           `json:"review_round"`
	ReviewerID            string        `json:"reviewer_id,omitempty"`
	Decision              string        `json:"decision"`
	Comment               string        `json:"comment,omitempty"`
	SignedAt              *time.Time    `json:"signed_at,omitempty"`
	Issues                []ReviewIssue `json:"issues"`
	Frozen                bool          `json:"frozen"`
	MaterialsDigest       string        `json:"materials_digest,omitempty"`
	IndependenceStatement string        `json:"independence_statement,omitempty"`
	Invalidated           bool          `json:"invalidated"`
	InvalidatedAt         *time.Time    `json:"invalidated_at,omitempty"`
	InvalidationReason    string        `json:"invalidation_reason,omitempty"`
}

type SuspensionInvestigation struct {
	CauseCategory   string    `json:"cause_category"`
	ImpactStartedAt time.Time `json:"impact_started_at"`
	ImpactEndedAt   time.Time `json:"impact_ended_at"`
	Action          string    `json:"action"`
	EvidenceRef     string    `json:"evidence_ref"`
	SubmittedBy     string    `json:"submitted_by"`
	SubmittedAt     time.Time `json:"submitted_at"`
}

type RecoveryAttempt struct {
	ReviewerID                string    `json:"reviewer_id"`
	ConfirmationObservationID string    `json:"confirmation_observation_id"`
	Verdict                   string    `json:"verdict"`
	FailureReason             string    `json:"failure_reason,omitempty"`
	DecidedAt                 time.Time `json:"decided_at"`
}

type TrialSuspension struct {
	SuspensionID         string                   `json:"suspension_id"`
	TriggerObservationID string                   `json:"trigger_observation_id"`
	ActualBias           float64                  `json:"actual_bias"`
	Threshold            float64                  `json:"threshold"`
	SuspendedAt          time.Time                `json:"suspended_at"`
	RollbackRequired     bool                     `json:"rollback_required"`
	State                string                   `json:"state"`
	ObservationActorID   string                   `json:"observation_actor_id"`
	Investigation        *SuspensionInvestigation `json:"investigation,omitempty"`
	RecoveryAttempts     []RecoveryAttempt        `json:"recovery_attempts"`
}

func (c *Case) CurrentEvidence(id string) (*Evidence, error) {
	for i := range c.Evidence {
		if c.Evidence[i].EvidenceID == id {
			return &c.Evidence[i], nil
		}
	}
	return nil, ErrNotFound
}

func (c *Case) CorrectEvidence(id, expectedDigest, reason, actor string, replacement Evidence, at time.Time) error {
	if err := c.RequireState(Draft); err != nil {
		return err
	}
	current, err := c.CurrentEvidence(id)
	if err != nil {
		return err
	}
	if expectedDigest == "" || current.ContentDigest != expectedDigest {
		return fmt.Errorf("%w: content_digest 不是证据当前版本", ErrConflict)
	}
	if strings.TrimSpace(reason) == "" {
		return fmt.Errorf("%w: correction_reason 必填", ErrInvalid)
	}
	old := *current
	if old.Version < 1 {
		old.Version = 1
	}
	hasVersion := false
	for _, v := range c.EvidenceVersions {
		if v.EvidenceID == id {
			hasVersion = true
			break
		}
	}
	if !hasVersion {
		c.EvidenceVersions = append(c.EvidenceVersions, EvidenceVersion{EvidenceID: id, Version: old.Version, ContentDigest: old.ContentDigest, ActorID: "legacy_import", CreatedAt: c.CreatedAt, Snapshot: old})
	}
	replacement.EvidenceID = id
	replacement.Version = old.Version + 1
	replacement.PreviousDigest = old.ContentDigest
	replacement.CorrectionReason = reason
	replacement.CorrectedBy = actor
	replacement.CorrectedAt = &at
	if err = ValidateEvidence(replacement); err != nil {
		return err
	}
	*current = replacement
	c.EvidenceVersions = append(c.EvidenceVersions, EvidenceVersion{EvidenceID: id, Version: replacement.Version, ContentDigest: replacement.ContentDigest, PreviousDigest: old.ContentDigest, CorrectionReason: reason, ActorID: actor, CreatedAt: at, Snapshot: replacement})
	return nil
}

func (c *Case) MarkBaselineVersions() {
	latest := map[string]string{}
	latestVersion := map[string]int{}
	decisions := map[string]string{}
	for _, e := range c.Evidence {
		latest[e.EvidenceID] = e.ContentDigest
		latestVersion[e.EvidenceID] = e.Version
		decisions[e.EvidenceID] = e.QualityDecision
	}
	for i := range c.EvidenceVersions {
		c.EvidenceVersions[i].IncludedInBaseline = latest[c.EvidenceVersions[i].EvidenceID] == c.EvidenceVersions[i].ContentDigest && latestVersion[c.EvidenceVersions[i].EvidenceID] == c.EvidenceVersions[i].Version
		if c.EvidenceVersions[i].IncludedInBaseline {
			c.EvidenceVersions[i].BaselineQualityDecision = decisions[c.EvidenceVersions[i].EvidenceID]
		}
	}
}

func (c *Case) ApplyQualityDecisions(changes []QualityDecisionChange) error {
	if err := c.RequireState(Draft); err != nil {
		return err
	}
	seen := map[string]bool{}
	indexes := map[string]int{}
	for i, e := range c.Evidence {
		indexes[e.EvidenceID] = i
	}
	for _, ch := range changes {
		if seen[ch.EvidenceID] {
			return fmt.Errorf("%w: evidence_id %s 重复", ErrInvalid, ch.EvidenceID)
		}
		seen[ch.EvidenceID] = true
		if _, ok := indexes[ch.EvidenceID]; !ok {
			return fmt.Errorf("%w: evidence_id %s 不存在", ErrInvalid, ch.EvidenceID)
		}
		if ch.Decision != "included" && ch.Decision != "excluded" && ch.Decision != "pending_explanation" {
			return fmt.Errorf("%w: decision %s 无效", ErrInvalid, ch.Decision)
		}
		if (ch.Decision == "excluded" || ch.Decision == "pending_explanation") && strings.TrimSpace(ch.Reason) == "" {
			return fmt.Errorf("%w: %s 决定必须填写原因", ErrInvalid, ch.Decision)
		}
	}
	for _, ch := range changes {
		i := indexes[ch.EvidenceID]
		c.Evidence[i].QualityDecision = ch.Decision
		c.Evidence[i].DecisionReason = ch.Reason
	}
	return nil
}

func (c *Case) EvidenceVersionChain(id string) ([]EvidenceVersion, error) {
	var out []EvidenceVersion
	for _, v := range c.EvidenceVersions {
		if v.EvidenceID == id {
			out = append(out, v)
		}
	}
	if len(out) == 0 {
		return nil, ErrNotFound
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Version < out[j].Version })
	return out, nil
}
