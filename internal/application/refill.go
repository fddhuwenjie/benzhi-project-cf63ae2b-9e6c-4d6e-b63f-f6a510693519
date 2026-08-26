package application

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"

	"benzhi-project-cf63ae2b-9e6c-4d6e-b63f-f6a510693519/internal/assessment"
	"benzhi-project-cf63ae2b-9e6c-4d6e-b63f-f6a510693519/internal/audit"
	"benzhi-project-cf63ae2b-9e6c-4d6e-b63f-f6a510693519/internal/domain"
	"benzhi-project-cf63ae2b-9e6c-4d6e-b63f-f6a510693519/internal/repository"
)

type CaseListInput struct {
	StationID, OwnerID string
	State              domain.State
	Archived           *bool
	Cursor             string
	Limit              int
}

func (s *Service) ListCases(ctx context.Context, in CaseListInput) (repository.CasePage, error) {
	if in.State != "" && !domain.QueryableStates[in.State] {
		return repository.CasePage{}, fmt.Errorf("%w: state 无效", domain.ErrInvalid)
	}
	return s.store.ListCases(ctx, repository.CaseListFilter{StationID: in.StationID, OwnerID: in.OwnerID, State: in.State, Archived: in.Archived, Cursor: in.Cursor, Limit: in.Limit})
}

type QualificationCorrectionInput struct {
	Meta
	PreviousDigest      string    `json:"previous_digest"`
	QualificationDigest string    `json:"qualification_digest"`
	CorrectionReason    string    `json:"correction_reason"`
	InstrumentID        string    `json:"instrument_id"`
	InstrumentKind      string    `json:"instrument_kind"`
	CertificateRef      string    `json:"certificate_ref"`
	CalibratedAt        time.Time `json:"calibrated_at"`
	ValidUntil          time.Time `json:"valid_until"`
	UsageStartedAt      time.Time `json:"usage_started_at"`
	UsageEndedAt        time.Time `json:"usage_ended_at"`
}

func (s *Service) CorrectQualification(ctx context.Context, caseID, id string, in QualificationCorrectionInput) (Result, error) {
	return s.mutateCommand(ctx, caseID, in.Meta, "instrument_qualification_corrected", "instrument_qualification_corrected/"+id, func(c *domain.Case, _ *repository.Tx) error {
		q := domain.Qualification{QualificationID: id, InstrumentID: in.InstrumentID, InstrumentKind: in.InstrumentKind, CertificateRef: in.CertificateRef, CalibratedAt: in.CalibratedAt, ValidUntil: in.ValidUntil, UsageStartedAt: in.UsageStartedAt, UsageEndedAt: in.UsageEndedAt}
		s.engine.QualifyInstrument(&q)
		digest := in.PreviousDigest
		if digest == "" {
			digest = in.QualificationDigest
		} else if in.QualificationDigest != "" && in.QualificationDigest != digest {
			return fmt.Errorf("%w: 两个资格摘要字段不一致", domain.ErrInvalid)
		}
		return c.CorrectQualification(id, digest, in.CorrectionReason, in.ActorID, q, s.now())
	})
}
func (s *Service) QualificationVersions(ctx context.Context, caseID, id string) (map[string]any, error) {
	c, err := s.store.LoadCase(ctx, caseID)
	if err != nil {
		return nil, err
	}
	v, err := c.QualificationVersionChain(id)
	if err != nil {
		return nil, err
	}
	return map[string]any{"qualification_id": id, "versions": v}, nil
}

type DueDateRevisionInput struct {
	Meta
	DueAt      time.Time `json:"due_at"`
	Reason     string    `json:"reason"`
	ApprovedBy string    `json:"approved_by"`
}

func (s *Service) ReviseDueDate(ctx context.Context, caseID, id string, in DueDateRevisionInput) (Result, error) {
	return s.mutateCommand(ctx, caseID, in.Meta, "deviation_due_date_revised", "deviation_due_date_revised/"+id, func(c *domain.Case, _ *repository.Tx) error {
		if err := c.RequireState(domain.Assessed); err != nil {
			return err
		}
		d, err := deviation(c, id)
		if err != nil {
			return err
		}
		if in.ApprovedBy == "" || in.ApprovedBy != in.ActorID || in.ApprovedBy == d.CorrectedBy {
			return fmt.Errorf("%w: 改期批准者必须独立于整改人", domain.ErrGate)
		}
		if strings.TrimSpace(in.Reason) == "" || !in.DueAt.After(d.DueAt) || !in.DueAt.After(s.now()) {
			return fmt.Errorf("%w: 改期原因必填且新期限必须更晚", domain.ErrInvalid)
		}
		if s.now().After(d.DueAt) {
			d.EverOverdue = true
		}
		d.DueDateRevisions = append(d.DueDateRevisions, domain.DueDateRevision{Version: len(d.DueDateRevisions) + 1, PreviousDueAt: d.DueAt, DueAt: in.DueAt.UTC(), Reason: in.Reason, ApprovedBy: in.ApprovedBy, ApprovedAt: s.now()})
		d.DueAt = in.DueAt.UTC()
		return nil
	})
}
func (s *Service) DeviationActionQueue(ctx context.Context, caseID string, asOf time.Time) (map[string]any, error) {
	if asOf.IsZero() {
		return nil, fmt.Errorf("%w: as_of 必填", domain.ErrInvalid)
	}
	s.deviationActionMu.Lock()
	if cached, ok := s.deviationActionQueues[caseID]; ok {
		actions := append([]domain.DeviationAction(nil), cached...)
		s.deviationActionMu.Unlock()
		return map[string]any{"case_id": caseID, "as_of": asOf.UTC(), "actions": actions}, nil
	}
	s.deviationActionMu.Unlock()
	c, err := s.store.LoadCase(ctx, caseID)
	if err != nil {
		return nil, err
	}
	actions := domain.DeviationQueue(c, asOf.UTC())
	s.deviationActionMu.Lock()
	s.deviationActionQueues[caseID] = append([]domain.DeviationAction(nil), actions...)
	s.deviationActionMu.Unlock()
	return map[string]any{"case_id": caseID, "as_of": asOf.UTC(), "actions": actions}, nil
}

func buildReviewPreflight(c *domain.Case, events []domain.AuditEvent, reviewer string) domain.ReviewPreflight {
	conflicts := []domain.RoleConflict{}
	if reviewer == c.OwnerID {
		conflicts = append(conflicts, domain.RoleConflict{Role: "owner", Source: "case"})
	}
	if reviewer == c.ModelerID {
		conflicts = append(conflicts, domain.RoleConflict{Role: "modeler", Source: "assessment"})
	}
	roles := map[string]string{"case_created": "case_creator", "evidence_registered": "evidence_submitter", "evidence_corrected": "evidence_corrector", "quality_decisions_rejudged": "quality_reviewer", "assessment_completed": "modeler", "deviation_corrected": "remediator", "deviation_retested": "deviation_verifier", "deviation_verified": "deviation_verifier"}
	for _, e := range events {
		if e.ActorID == reviewer && roles[e.EventType] != "" {
			conflicts = append(conflicts, domain.RoleConflict{Role: roles[e.EventType], Source: e.EventType, EventID: e.EventID})
		}
	}
	sort.Slice(conflicts, func(i, j int) bool {
		if conflicts[i].Role == conflicts[j].Role {
			return conflicts[i].EventID < conflicts[j].EventID
		}
		return conflicts[i].Role < conflicts[j].Role
	})
	closed := c.AllDeviationsClosed()
	gates := map[string]bool{"state_deviations_closed": c.State == domain.DeviationsClosed, "deviations_closed": closed, "assessment_present": c.Assessment != nil}
	last := ""
	if len(events) > 0 {
		last = events[len(events)-1].EventDigest
	}
	materials := domain.Digest(struct {
		Revision              int64  `json:"revision"`
		Baseline              string `json:"baseline_digest"`
		Assessment            any    `json:"assessment"`
		Deviations            any    `json:"deviations"`
		EvidenceVersions      any    `json:"evidence_versions"`
		QualificationVersions any    `json:"qualification_versions"`
		LastEvent             string `json:"last_event_digest"`
	}{c.Revision, c.BaselineDigest, c.Assessment, c.Deviations, c.EvidenceVersions, c.QualificationVersions, last})
	eligible := reviewer != "" && len(conflicts) == 0
	for _, v := range gates {
		eligible = eligible && v
	}
	return domain.ReviewPreflight{ReviewerID: reviewer, Revision: c.Revision, Conflicts: conflicts, Gates: gates, MaterialsDigest: materials, Eligible: eligible}
}
func (s *Service) ReviewPreflight(ctx context.Context, caseID, reviewer string) (domain.ReviewPreflight, error) {
	if strings.TrimSpace(reviewer) == "" || len(reviewer) > 80 {
		return domain.ReviewPreflight{}, fmt.Errorf("%w: reviewer_id 必填且不超过 80 字符", domain.ErrInvalid)
	}
	c, err := s.store.LoadCase(ctx, caseID)
	if err != nil {
		return domain.ReviewPreflight{}, err
	}
	events, err := s.store.Events(ctx, caseID)
	if err != nil {
		return domain.ReviewPreflight{}, err
	}
	return buildReviewPreflight(c, events, reviewer), nil
}

func trialRestrictedActors(c *domain.Case) []string {
	out := []string{c.OwnerID, c.ModelerID}
	if c.Review != nil {
		out = append(out, c.Review.ReviewerID)
	}
	for _, r := range c.ReviewRounds {
		out = append(out, r.ReviewerID)
	}
	for _, d := range c.Decisions {
		if d.DecisionType == "trial" {
			out = append(out, d.AuthorizedBy)
		}
	}
	return out
}
func refreshTrialContributions(c *domain.Case) {
	currentDecisionID := ""
	trialCount := 0
	for _, decision := range c.Decisions {
		if decision.DecisionType == "trial" {
			trialCount++
			if decision.Status != "invalidated" && decision.Status != "expired_unqualified" && decision.Status != "qualified" {
				currentDecisionID = decision.DecisionID
			}
		}
	}
	for i := range c.TrialObservations {
		o := &c.TrialObservations[i]
		o.CountsTowardProgress = o.Verdict == "continue" && o.RecordState != "superseded"
		if currentDecisionID != "" && o.TrialDecisionID != currentDecisionID && !(trialCount == 1 && o.TrialDecisionID == "") {
			o.CountsTowardProgress = false
		}
		if c.Assessment != nil {
			o.Band = assessment.TrialBand(c.Assessment, o.WaterLevelM)
		}
		for _, x := range c.TrialSuspensions {
			if x.Investigation != nil && !o.ObservedAt.Before(x.Investigation.ImpactStartedAt) && !o.ObservedAt.After(x.Investigation.ImpactEndedAt) {
				o.CountsTowardProgress = false
			}
		}
	}
}
func (s *Service) TrialProgress(ctx context.Context, caseID string) (domain.TrialProgress, error) {
	c, err := s.store.LoadCase(ctx, caseID)
	if err != nil {
		return domain.TrialProgress{}, err
	}
	if c.Assessment == nil {
		return domain.TrialProgress{}, fmt.Errorf("%w: 缺少评估边界", domain.ErrGate)
	}
	refreshTrialContributions(c)
	return s.engine.TrialProgress(c), nil
}

type ArchiveVerification struct {
	ArchiveDigest    string `json:"archive_digest"`
	SuppliedDigest   string `json:"supplied_digest,omitempty"`
	IntegrityOK      bool   `json:"integrity_ok"`
	IntegrityMessage string `json:"integrity_message"`
	Kind             string `json:"kind,omitempty"`
	ID               string `json:"id,omitempty"`
	ItemDigest       string `json:"item_digest,omitempty"`
	ManifestPosition *int   `json:"manifest_position,omitempty"`
}

func (s *Service) VerifyArchive(ctx context.Context, caseID, supplied, kind, id string) (ArchiveVerification, error) {
	a, err := s.store.Archive(ctx, caseID)
	if err != nil {
		return ArchiveVerification{}, err
	}
	ok, msg := audit.VerifyArchive(a)
	if supplied != "" && supplied != a.Digest {
		ok = false
		msg = "调用方保存的 archive_digest 不匹配"
	}
	out := ArchiveVerification{ArchiveDigest: a.Digest, SuppliedDigest: supplied, IntegrityOK: ok, IntegrityMessage: msg, Kind: kind, ID: id}
	if kind != "" || id != "" {
		if kind == "" || id == "" {
			return ArchiveVerification{}, fmt.Errorf("%w: kind 和 id 必须同时提供", domain.ErrInvalid)
		}
		for i, x := range a.Manifest {
			if x.Kind == kind && x.ID == id {
				p := i
				out.ItemDigest = x.Digest
				out.ManifestPosition = &p
				return out, nil
			}
		}
		return ArchiveVerification{}, domain.ErrNotFound
	}
	return out, nil
}
