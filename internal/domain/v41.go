package domain

import (
	"fmt"
	"sort"
	"strings"
	"time"
)

type InstrumentImpact struct {
	EvidenceID      string    `json:"evidence_id"`
	InstrumentKind  string    `json:"instrument_kind"`
	ObservedAt      time.Time `json:"observed_at"`
	QualificationID string    `json:"qualification_id"`
	CoverageFailure string    `json:"coverage_failure_reason"`
}

type InstrumentInvalidation struct {
	InvalidationID            string             `json:"invalidation_id"`
	QualificationID           string             `json:"qualification_id"`
	InvalidationType          string             `json:"invalidation_type"`
	InvalidatedAt             time.Time          `json:"invalidated_at"`
	Reason                    string             `json:"reason"`
	NotificationEvidenceRef   string             `json:"notification_evidence_ref"`
	OriginalCertificateDigest string             `json:"original_certificate_digest"`
	AffectedBindings          []InstrumentImpact `json:"affected_bindings"`
	DeviationID               string             `json:"deviation_id,omitempty"`
	ReportedBy                string             `json:"reported_by"`
	ReportedAt                time.Time          `json:"reported_at"`
	CoverageRestored          bool               `json:"coverage_restored"`
	RestorationEvidenceRef    string             `json:"restoration_evidence_ref,omitempty"`
}

type TrialExpirySettlement struct {
	SettlementID      string        `json:"settlement_id"`
	DecisionID        string        `json:"decision_id"`
	EffectiveUntil    time.Time     `json:"effective_until"`
	SettledAt         time.Time     `json:"settled_at"`
	Verdict           string        `json:"verdict"`
	Progress          TrialProgress `json:"progress"`
	UnmetGates        []string      `json:"unmet_gates"`
	ActiveSuspensions []string      `json:"active_suspensions"`
	NextAction        string        `json:"next_action"`
}

func (c *Case) FrozenQualification(id string) (Qualification, error) {
	items, err := c.FrozenQualifications()
	if err != nil {
		return Qualification{}, err
	}
	for _, q := range items {
		if q.QualificationID == id {
			return q, nil
		}
	}
	return Qualification{}, ErrNotFound
}

func (c *Case) ValidateInstrumentInvalidation(q Qualification, typ string, at time.Time, reason, evidenceRef, digest string) error {
	if err := c.RequireState(Assessed, DeviationsClosed, Reviewed, TrialActive, TrialSuspended, TrialQualified); err != nil {
		return err
	}
	allowed := map[string]bool{"revoked": true, "voided": true, "traceability_interrupted": true}
	if !allowed[typ] || at.IsZero() || strings.TrimSpace(reason) == "" || strings.TrimSpace(evidenceRef) == "" || strings.TrimSpace(digest) == "" {
		return fmt.Errorf("%w: invalidation_type、invalidated_at、reason、notification_evidence_ref 和 original_certificate_digest 必填", ErrInvalid)
	}
	if digest != q.Digest {
		return fmt.Errorf("%w: original_certificate_digest 与冻结资格摘要不符", ErrConflict)
	}
	if at.Before(q.CalibratedAt) {
		return fmt.Errorf("%w: invalidated_at 不得早于 calibrated_at", ErrInvalid)
	}
	for _, x := range c.InstrumentInvalidations {
		if x.QualificationID == q.QualificationID {
			return fmt.Errorf("%w: qualification_id 已登记失效通报", ErrConflict)
		}
	}
	return nil
}

func (c *Case) LatestOpenTrial() (*Decision, error) {
	var found *Decision
	for i := range c.Decisions {
		d := &c.Decisions[i]
		if d.DecisionType != "trial" || d.Status == "invalidated" || d.Status == "expired_unqualified" || d.Status == "qualified" {
			continue
		}
		if found != nil {
			return nil, fmt.Errorf("%w: multiple_unsettled_trial_decisions", ErrGate)
		}
		found = d
	}
	if found == nil {
		return nil, fmt.Errorf("%w: trial_decision_missing", ErrGate)
	}
	if found.EffectiveUntil == nil {
		return nil, fmt.Errorf("%w: trial_effective_until_missing", ErrGate)
	}
	return found, nil
}

func (c *Case) SettlementFor(decisionID string) *TrialExpirySettlement {
	for i := range c.TrialExpirySettlements {
		if c.TrialExpirySettlements[i].DecisionID == decisionID {
			return &c.TrialExpirySettlements[i]
		}
	}
	return nil
}

func SortedStrings(values []string) []string {
	out := append([]string(nil), values...)
	sort.Strings(out)
	return out
}
