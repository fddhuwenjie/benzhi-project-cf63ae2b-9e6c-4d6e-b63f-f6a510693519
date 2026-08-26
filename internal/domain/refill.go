package domain

import (
	"fmt"
	"math"
	"sort"
	"strings"
	"time"
)

var QueryableStates = map[State]bool{
	Draft: true, BaselineFrozen: true, EvidenceQualified: true, Assessed: true,
	DeviationsClosed: true, Reviewed: true, TrialActive: true, TrialSuspended: true,
	TrialQualified: true, Activated: true, Archived: true,
}

type CandidateConflictError struct {
	CaseID   string `json:"case_id"`
	State    State  `json:"state"`
	Revision int64  `json:"revision"`
}

func (e *CandidateConflictError) Error() string {
	return fmt.Sprintf("%s: duplicate_candidate case_id=%s state=%s revision=%d", ErrConflict, e.CaseID, e.State, e.Revision)
}
func (e *CandidateConflictError) Is(target error) bool { return target == ErrConflict }

type CaseListItem struct {
	CaseID                string    `json:"case_id"`
	StationID             string    `json:"station_id"`
	CandidateVersion      string    `json:"candidate_version"`
	OwnerID               string    `json:"owner_id"`
	State                 State     `json:"state"`
	Revision              int64     `json:"revision"`
	CreatedAt             time.Time `json:"created_at"`
	Archived              bool      `json:"archived"`
	EvidenceCount         int       `json:"evidence_count"`
	OpenDeviationCount    int       `json:"open_deviation_count"`
	TrialObservationCount int       `json:"trial_observation_count"`
	NextGate              string    `json:"next_gate"`
}

func NextGate(c *Case) string {
	switch c.State {
	case Draft:
		return "freeze_baseline"
	case BaselineFrozen:
		return "qualify_evidence"
	case EvidenceQualified:
		return "run_assessment"
	case Assessed:
		return "close_deviations"
	case DeviationsClosed:
		return "independent_review"
	case Reviewed:
		return "issue_trial"
	case TrialActive:
		return "trial_coverage"
	case TrialSuspended:
		return "trial_recovery"
	case TrialQualified:
		return "activation"
	case Activated:
		return "archive"
	default:
		return "none"
	}
}

func ProjectCase(c *Case) CaseListItem {
	open := 0
	for _, d := range c.Deviations {
		if d.State != "verified" {
			open++
		}
	}
	return CaseListItem{c.CaseID, c.StationID, c.CandidateVersion, c.OwnerID, c.State,
		c.Revision, c.CreatedAt, c.State == Archived, len(c.Evidence), open,
		len(c.TrialObservations), NextGate(c)}
}

type QualificationVersion struct {
	QualificationID  string        `json:"qualification_id"`
	Version          int           `json:"version"`
	Digest           string        `json:"digest"`
	PreviousDigest   string        `json:"previous_digest,omitempty"`
	CorrectionReason string        `json:"correction_reason,omitempty"`
	ActorID          string        `json:"actor_id"`
	CreatedAt        time.Time     `json:"created_at"`
	Verdict          string        `json:"verdict"`
	Snapshot         Qualification `json:"snapshot"`
}

func QualificationDigest(q Qualification) string {
	q.Digest, q.PreviousDigest, q.CorrectionReason, q.CorrectedBy, q.CorrectedAt = "", "", "", "", nil
	return Digest(q)
}

func (c *Case) Qualification(id string) (*Qualification, error) {
	for i := range c.Qualifications {
		if c.Qualifications[i].QualificationID == id {
			return &c.Qualifications[i], nil
		}
	}
	return nil, ErrNotFound
}

func (c *Case) CorrectQualification(id, expectedDigest, reason, actor string, replacement Qualification, at time.Time) error {
	if err := c.RequireState(Draft); err != nil {
		return err
	}
	current, err := c.Qualification(id)
	if err != nil {
		return err
	}
	if expectedDigest == "" || expectedDigest != current.Digest {
		return fmt.Errorf("%w: qualification_digest 不是当前版本", ErrConflict)
	}
	if strings.TrimSpace(reason) == "" {
		return fmt.Errorf("%w: correction_reason 必填", ErrInvalid)
	}
	old := *current
	replacement.QualificationID, replacement.Version = id, old.Version+1
	replacement.PreviousDigest, replacement.CorrectionReason = old.Digest, reason
	replacement.CorrectedBy, replacement.CorrectedAt = actor, &at
	replacement.Digest = QualificationDigest(replacement)
	if err := ValidateQualification(replacement); err != nil {
		return err
	}
	for _, q := range c.Qualifications {
		if q.QualificationID != id && q.InstrumentID == replacement.InstrumentID && q.CertificateRef != replacement.CertificateRef && intervalsOverlap(q.CalibratedAt, q.ValidUntil, replacement.CalibratedAt, replacement.ValidUntil) {
			return fmt.Errorf("%w: instrument_id %s 存在相互矛盾的有效证书区间", ErrInvalid, replacement.InstrumentID)
		}
	}
	*current = replacement
	c.QualificationVersions = append(c.QualificationVersions, QualificationVersion{id, replacement.Version, replacement.Digest, old.Digest, reason, actor, at, replacement.Verdict, replacement})
	return nil
}

func (c *Case) QualificationVersionChain(id string) ([]QualificationVersion, error) {
	items := []QualificationVersion{}
	for _, v := range c.QualificationVersions {
		if v.QualificationID == id {
			items = append(items, v)
		}
	}
	if len(items) == 0 {
		return nil, ErrNotFound
	}
	sort.Slice(items, func(i, j int) bool { return items[i].Version < items[j].Version })
	return items, nil
}

type InfluenceDetail struct {
	EvidenceID                  string  `json:"evidence_id"`
	ParameterChangeRatio        float64 `json:"parameter_change_ratio"`
	TargetPredictionChangeRatio float64 `json:"target_prediction_change_ratio"`
	UpperBoundChangeM           float64 `json:"upper_bound_change_m"`
	InfluenceScore              float64 `json:"influence_score"`
	Threshold                   float64 `json:"threshold"`
	Verdict                     string  `json:"verdict"`
}

type FloodMarkConstraint struct {
	EvidenceID   string  `json:"evidence_id"`
	FloodEventID string  `json:"flood_event_id"`
	LowerM       float64 `json:"lower_m"`
	UpperM       float64 `json:"upper_m"`
	Adopted      bool    `json:"adopted"`
	Reason       string  `json:"reason"`
}

type DueDateRevision struct {
	Version       int       `json:"version"`
	PreviousDueAt time.Time `json:"previous_due_at"`
	DueAt         time.Time `json:"due_at"`
	Reason        string    `json:"reason"`
	ApprovedBy    string    `json:"approved_by"`
	ApprovedAt    time.Time `json:"approved_at"`
}

func DeviationDueAt(severity string, discovered time.Time) time.Time {
	d := map[string]time.Duration{"critical": 24 * time.Hour, "major": 7 * 24 * time.Hour, "minor": 30 * 24 * time.Hour}[severity]
	return discovered.UTC().Add(d)
}

func InitializeDeviation(d *Deviation, actor string, at time.Time) {
	d.CreatedBy, d.CreatedAt = actor, at.UTC()
	d.OriginalDueAt = DeviationDueAt(d.Severity, at)
	d.DueAt = d.OriginalDueAt
	d.DueDateRevisions = []DueDateRevision{}
	if d.PhaseHistory == nil {
		d.PhaseHistory = []DeviationPhase{{State: "open", ActorID: actor, OccurredAt: at.UTC(), Description: "偏差建立"}}
	}
	if d.Retests == nil {
		d.Retests = []DeviationRetest{}
	}
}

type DeviationAction struct {
	DeviationID      string            `json:"deviation_id"`
	Severity         string            `json:"severity"`
	State            string            `json:"state"`
	Action           string            `json:"action"`
	OriginalDueAt    time.Time         `json:"original_due_at"`
	DueAt            time.Time         `json:"due_at"`
	Overdue          bool              `json:"overdue"`
	NearDue          bool              `json:"near_due"`
	OverdueBySeconds int64             `json:"overdue_by_seconds"`
	EverOverdue      bool              `json:"ever_overdue"`
	DueDateRevisions []DueDateRevision `json:"due_date_revisions"`
	CurrentAttemptNo int               `json:"current_attempt_no"`
	FailedAttempts   int               `json:"failed_attempts"`
}

func DeviationQueue(c *Case, asOf time.Time) []DeviationAction {
	out := []DeviationAction{}
	for _, d := range c.Deviations {
		action := map[string]string{"open": "containment", "contained": "root_cause", "analyzed": "correction", "corrected": "verification", "correction_required": "correction", "verified": "complete"}[d.State]
		overdue := d.State != "verified" && asOf.After(d.DueAt)
		near := d.State != "verified" && !overdue && !asOf.Before(d.DueAt.Add(-24*time.Hour))
		seconds := int64(0)
		if overdue {
			seconds = int64(asOf.Sub(d.DueAt).Seconds())
		}
		failed := 0
		for _, x := range d.CorrectionAttempts {
			if x.Verification != nil && x.Verification.Verdict == "fail" {
				failed++
			}
		}
		out = append(out, DeviationAction{DeviationID: d.DeviationID, Severity: d.Severity, State: d.State, Action: action, OriginalDueAt: d.OriginalDueAt, DueAt: d.DueAt, Overdue: overdue, NearDue: near, OverdueBySeconds: seconds, EverOverdue: d.EverOverdue || overdue, DueDateRevisions: append([]DueDateRevision(nil), d.DueDateRevisions...), CurrentAttemptNo: len(d.CorrectionAttempts), FailedAttempts: failed})
	}
	rank := map[string]int{"critical": 0, "major": 1, "minor": 2}
	sort.Slice(out, func(i, j int) bool {
		if out[i].OverdueBySeconds != out[j].OverdueBySeconds {
			return out[i].OverdueBySeconds > out[j].OverdueBySeconds
		}
		if rank[out[i].Severity] != rank[out[j].Severity] {
			return rank[out[i].Severity] < rank[out[j].Severity]
		}
		return out[i].DeviationID < out[j].DeviationID
	})
	return out
}

type RoleConflict struct {
	Role    string `json:"role"`
	Source  string `json:"source"`
	EventID string `json:"event_id,omitempty"`
}
type ReviewPreflight struct {
	ReviewerID      string          `json:"reviewer_id"`
	Revision        int64           `json:"revision"`
	Conflicts       []RoleConflict  `json:"conflicts"`
	Gates           map[string]bool `json:"gates"`
	MaterialsDigest string          `json:"materials_digest"`
	Eligible        bool            `json:"eligible"`
}

type TrialBandProgress struct {
	Band         string `json:"band"`
	ValidSamples int    `json:"valid_samples"`
}
type TrialProgress struct {
	Bands                 []TrialBandProgress `json:"bands"`
	ValidSamples          int                 `json:"valid_samples"`
	IndependentSubmitters int                 `json:"independent_submitters"`
	DurationHours         float64             `json:"duration_hours"`
	MaximumAbsoluteBias   float64             `json:"maximum_absolute_bias"`
	UnmetGates            []string            `json:"unmet_gates"`
	Qualified             bool                `json:"qualified"`
}

func TrialProgressFor(c *Case, minimumSamples int, minimumDuration time.Duration, biasLimit float64) TrialProgress {
	counts := map[string]int{"low": 0, "medium": 0, "high": 0}
	actors := map[string]bool{}
	valid := []TrialObservation{}
	maxBias := 0.0
	for _, o := range c.TrialObservations {
		if o.CountsTowardProgress && o.Verdict == "continue" {
			valid = append(valid, o)
			counts[o.Band]++
			actors[o.SubmittedBy] = true
			maxBias = math.Max(maxBias, math.Abs(o.RelativeBias))
		}
	}
	sort.Slice(valid, func(i, j int) bool { return valid[i].ObservedAt.Before(valid[j].ObservedAt) })
	dur := time.Duration(0)
	if len(valid) > 1 {
		dur = valid[len(valid)-1].ObservedAt.Sub(valid[0].ObservedAt)
	}
	unmet := []string{}
	if len(valid) < minimumSamples {
		unmet = append(unmet, "minimum_samples")
	}
	if dur < minimumDuration {
		unmet = append(unmet, "minimum_duration")
	}
	if maxBias > biasLimit {
		unmet = append(unmet, "maximum_bias")
	}
	if len(actors) < 2 {
		unmet = append(unmet, "minimum_independent_submitters")
	}
	for _, band := range []string{"low", "medium", "high"} {
		if counts[band] == 0 {
			unmet = append(unmet, band+"_band_coverage")
		}
	}
	return TrialProgress{[]TrialBandProgress{{"low", counts["low"]}, {"medium", counts["medium"]}, {"high", counts["high"]}}, len(valid), len(actors), dur.Hours(), maxBias, unmet, len(unmet) == 0}
}

func intervalsOverlap(a1, a2, b1, b2 time.Time) bool { return !a1.After(b2) && !b1.After(a2) }
