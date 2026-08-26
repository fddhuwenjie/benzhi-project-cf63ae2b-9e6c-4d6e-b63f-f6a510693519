package domain

import (
	"fmt"
	"math"
	"sort"
	"strings"
	"time"
)

type InstrumentBinding struct {
	InstrumentKind    string `json:"instrument_kind"`
	InstrumentID      string `json:"instrument_id"`
	QualificationID   string `json:"qualification_id"`
	CertificateDigest string `json:"certificate_digest"`
}

type BaselineItem struct {
	Kind          string         `json:"kind"`
	ID            string         `json:"id"`
	Version       int            `json:"version"`
	VersionDigest string         `json:"version_digest"`
	Evidence      *Evidence      `json:"evidence,omitempty"`
	Qualification *Qualification `json:"qualification,omitempty"`
}

type BaselineManifest struct {
	Digest    string         `json:"digest"`
	CreatedAt time.Time      `json:"created_at"`
	Revision  int64          `json:"revision"`
	Items     []BaselineItem `json:"items"`
	GateCodes []string       `json:"gate_codes"`
}

type ResourceIssue struct {
	Code               string `json:"code"`
	Index              *int   `json:"index,omitempty"`
	EvidenceID         string `json:"evidence_id,omitempty"`
	ExistingEvidenceID string `json:"existing_evidence_id,omitempty"`
	QualificationID    string `json:"qualification_id,omitempty"`
	InstrumentKind     string `json:"instrument_kind,omitempty"`
	Message            string `json:"message"`
}

type StructuredError struct {
	Kind   error           `json:"-"`
	Issues []ResourceIssue `json:"issues"`
}

func (e *StructuredError) Error() string {
	return fmt.Sprintf("%v: %d 个问题", e.Kind, len(e.Issues))
}
func (e *StructuredError) Unwrap() error { return e.Kind }

type BoundaryDiagnostic struct {
	Side               string  `json:"side"`
	RequestedBoundM    float64 `json:"requested_bound_m"`
	AllowedBoundM      float64 `json:"allowed_bound_m"`
	AdoptedBoundM      float64 `json:"adopted_bound_m"`
	ExtrapolationRatio float64 `json:"extrapolation_ratio"`
	LimitSource        string  `json:"limit_source"`
	ExceedanceM        float64 `json:"exceedance_m"`
	Verdict            string  `json:"verdict"`
}

type CorrectionAttempt struct {
	AttemptNo     int              `json:"attempt_no"`
	Description   string           `json:"description"`
	EvidenceRefs  []string         `json:"evidence_refs"`
	CorrectedBy   string           `json:"corrected_by"`
	CorrectedAt   time.Time        `json:"corrected_at"`
	FailureChange string           `json:"failure_change,omitempty"`
	Verification  *DeviationRetest `json:"verification,omitempty"`
}

func RequiredInstrumentKinds(evidenceType string) []string {
	switch evidenceType {
	case "rating_measurement":
		return []string{"current_meter", "water_level_gauge"}
	case "cross_section":
		return []string{"survey_equipment"}
	default:
		return nil
	}
}

func ValidateBindings(e Evidence, qualifications []Qualification, requireAll bool) error {
	required := RequiredInstrumentKinds(e.EvidenceType)
	seen := map[string]bool{}
	byID := map[string]Qualification{}
	for _, q := range qualifications {
		byID[q.QualificationID] = q
	}
	for _, b := range e.InstrumentBindings {
		if seen[b.InstrumentKind] {
			return fmt.Errorf("%w: instrument_kind %s 重复绑定", ErrInvalid, b.InstrumentKind)
		}
		seen[b.InstrumentKind] = true
		q, ok := byID[b.QualificationID]
		if !ok {
			return fmt.Errorf("%w: qualification_id %s 不属于本案", ErrInvalid, b.QualificationID)
		}
		if q.InstrumentID != b.InstrumentID || q.InstrumentKind != b.InstrumentKind {
			return fmt.Errorf("%w: 绑定设备声明与资格不一致", ErrInvalid)
		}
		if b.CertificateDigest == "" || b.CertificateDigest != q.Digest {
			return fmt.Errorf("%w: certificate_digest 与资格当前版本不一致", ErrInvalid)
		}
	}
	if requireAll {
		for _, kind := range required {
			if !seen[kind] {
				return fmt.Errorf("%w: 证据 %s 缺少 %s 绑定", ErrGate, e.EvidenceID, kind)
			}
		}
		if len(seen) != len(required) {
			return fmt.Errorf("%w: 证据 %s 含非必需仪器绑定", ErrInvalid, e.EvidenceID)
		}
	}
	return nil
}

func ValidateStoredBindingReferences(e Evidence, qualifications []Qualification) error {
	byID := map[string]Qualification{}
	for _, q := range qualifications {
		byID[q.QualificationID] = q
	}
	for _, b := range e.InstrumentBindings {
		q, ok := byID[b.QualificationID]
		if !ok {
			return fmt.Errorf("%w: qualification_id %s 不属于本案", ErrInvalid, b.QualificationID)
		}
		if q.InstrumentID != b.InstrumentID || q.InstrumentKind != b.InstrumentKind {
			return fmt.Errorf("%w: 绑定设备声明与资格不一致", ErrInvalid)
		}
	}
	return nil
}

func BuildBaselineManifest(c *Case, at time.Time) BaselineManifest {
	items := make([]BaselineItem, 0, len(c.Evidence)+len(c.Qualifications))
	for i := range c.Evidence {
		x := c.Evidence[i]
		x.InstrumentBindings = append([]InstrumentBinding(nil), x.InstrumentBindings...)
		sort.Slice(x.InstrumentBindings, func(i, j int) bool {
			if x.InstrumentBindings[i].InstrumentKind == x.InstrumentBindings[j].InstrumentKind {
				return x.InstrumentBindings[i].QualificationID < x.InstrumentBindings[j].QualificationID
			}
			return x.InstrumentBindings[i].InstrumentKind < x.InstrumentBindings[j].InstrumentKind
		})
		items = append(items, BaselineItem{Kind: "evidence", ID: x.EvidenceID, Version: x.Version, VersionDigest: x.ContentDigest, Evidence: &x})
	}
	for i := range c.Qualifications {
		x := c.Qualifications[i]
		items = append(items, BaselineItem{Kind: "qualification", ID: x.QualificationID, Version: x.Version, VersionDigest: x.Digest, Qualification: &x})
	}
	sort.Slice(items, func(i, j int) bool {
		if items[i].Kind == items[j].Kind {
			return items[i].ID < items[j].ID
		}
		return items[i].Kind < items[j].Kind
	})
	digest := Digest(struct {
		Items []BaselineItem `json:"items"`
	}{items})
	return BaselineManifest{Digest: digest, CreatedAt: at.UTC(), Revision: c.Revision, Items: items, GateCodes: []string{}}
}

func (c *Case) FrozenEvidence() ([]Evidence, error) {
	if c.BaselineManifest == nil {
		return nil, fmt.Errorf("%w: baseline_manifest_missing", ErrGate)
	}
	out := []Evidence{}
	for _, x := range c.BaselineManifest.Items {
		if x.Kind != "evidence" {
			continue
		}
		if x.Evidence == nil || x.Evidence.ContentDigest != x.VersionDigest || x.Evidence.Version != x.Version {
			return nil, fmt.Errorf("%w: baseline_evidence_version_mismatch %s", ErrGate, x.ID)
		}
		out = append(out, *x.Evidence)
	}
	return out, nil
}

func (c *Case) FrozenQualifications() ([]Qualification, error) {
	if c.BaselineManifest == nil {
		return nil, fmt.Errorf("%w: baseline_manifest_missing", ErrGate)
	}
	out := []Qualification{}
	for _, x := range c.BaselineManifest.Items {
		if x.Kind != "qualification" {
			continue
		}
		if x.Qualification == nil || x.Qualification.Digest != x.VersionDigest || x.Qualification.Version != x.Version {
			return nil, fmt.Errorf("%w: baseline_qualification_version_mismatch %s", ErrGate, x.ID)
		}
		out = append(out, *x.Qualification)
	}
	return out, nil
}

func ValidateBoundaryRequest(lower, upper, lowRatio, highRatio float64) error {
	for _, v := range []float64{lower, upper, lowRatio, highRatio} {
		if math.IsNaN(v) || math.IsInf(v, 0) {
			return fmt.Errorf("%w: 边界参数必须为有限数", ErrInvalid)
		}
	}
	if lower >= upper {
		return fmt.Errorf("%w: requested_lower_bound_m 必须小于 requested_upper_bound_m", ErrInvalid)
	}
	if lowRatio < 0 || highRatio < 0 {
		return fmt.Errorf("%w: 两端最大延伸比例不得为负数", ErrInvalid)
	}
	return nil
}

func SameStringSet(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	aa, bb := append([]string(nil), a...), append([]string(nil), b...)
	sort.Strings(aa)
	sort.Strings(bb)
	for i := range aa {
		if strings.TrimSpace(aa[i]) == "" || aa[i] != bb[i] {
			return false
		}
	}
	return true
}

var AuditEventTypes = map[string]bool{
	"case_created": true, "evidence_registered": true, "evidence_batch_registered": true, "evidence_corrected": true, "quality_decisions_rejudged": true,
	"instrument_qualified": true, "instrument_qualification_corrected": true, "baseline_frozen": true, "evidence_qualified": true, "assessment_completed": true,
	"deviation_opened": true, "deviation_contained": true, "deviation_analyzed": true, "deviation_corrected": true, "deviation_retested": true, "deviation_verified": true, "deviation_due_date_revised": true, "deviations_closed": true,
	"independent_review_signed": true, "review_issue_responded": true, "review_resubmitted": true, "trial_issued": true, "trial_observation_registered": true, "trial_observation_replaced": true, "trial_suspension_investigated": true, "trial_recovery_decided": true, "curve_activated": true, "case_archived": true,
	"instrument_certificate_invalidated": true, "trial_expiry_settled": true,
}
