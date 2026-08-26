package application

import (
	"context"
	"encoding/base64"
	"fmt"
	"math"
	"sort"
	"strconv"
	"strings"
	"time"

	"benzhi-project-cf63ae2b-9e6c-4d6e-b63f-f6a510693519/internal/assessment"
	"benzhi-project-cf63ae2b-9e6c-4d6e-b63f-f6a510693519/internal/audit"
	"benzhi-project-cf63ae2b-9e6c-4d6e-b63f-f6a510693519/internal/domain"
	"benzhi-project-cf63ae2b-9e6c-4d6e-b63f-f6a510693519/internal/repository"
)

type EvidenceBatchItem struct {
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
type EvidenceBatchInput struct {
	Meta
	Evidence []EvidenceBatchItem `json:"evidence"`
}
type EvidenceBatchResponse struct {
	CaseID      string   `json:"case_id"`
	EvidenceIDs []string `json:"evidence_ids"`
	BatchDigest string   `json:"batch_digest"`
	Count       int      `json:"count"`
	Revision    int64    `json:"revision"`
}

func evidenceFromBatch(x EvidenceBatchItem) domain.Evidence {
	decision := x.QualityDecision
	if decision == "" {
		decision = "pending"
	}
	return domain.Evidence{EvidenceID: x.EvidenceID, EvidenceType: x.EvidenceType, ObservedAt: x.ObservedAt, WaterLevelM: x.WaterLevelM, DischargeM3S: x.DischargeM3S, SourceRef: x.SourceRef, ContentDigest: domain.ContentDigest(x.Content), QualityDecision: decision, DecisionReason: x.DecisionReason, Version: 1, FloodEventID: x.FloodEventID, VerticalUncertaintyM: x.VerticalUncertaintyM, DatumID: x.DatumID, ConfidenceLevel: x.ConfidenceLevel, InstrumentBindings: x.InstrumentBindings}
}

func validateEvidenceBatch(c *domain.Case, items []EvidenceBatchItem) ([]domain.Evidence, []domain.ResourceIssue) {
	issues := []domain.ResourceIssue{}
	out := make([]domain.Evidence, len(items))
	ids, digests, measurements := map[string]string{}, map[string]string{}, map[string]string{}
	for _, e := range c.Evidence {
		ids[e.EvidenceID] = e.EvidenceID
		digests[e.ContentDigest] = e.EvidenceID
		if e.EvidenceType == "rating_measurement" && e.WaterLevelM != nil && e.DischargeM3S != nil {
			measurements[fmt.Sprintf("%s/%.9f/%.9f", e.ObservedAt.UTC().Format(time.RFC3339Nano), *e.WaterLevelM, *e.DischargeM3S)] = e.EvidenceID
		}
	}
	for i, x := range items {
		e := evidenceFromBatch(x)
		out[i] = e
		index := i
		if strings.TrimSpace(x.Content) == "" {
			issues = append(issues, domain.ResourceIssue{Code: "content_required", Index: &index, EvidenceID: x.EvidenceID, Message: "content 必填"})
			continue
		}
		if err := domain.ValidateEvidence(e); err != nil {
			issues = append(issues, domain.ResourceIssue{Code: "invalid_evidence", Index: &index, EvidenceID: x.EvidenceID, Message: err.Error()})
			continue
		}
		if err := domain.ValidateBindings(e, c.Qualifications, true); err != nil {
			issues = append(issues, domain.ResourceIssue{Code: "invalid_instrument_binding", Index: &index, EvidenceID: x.EvidenceID, Message: err.Error()})
		}
		if prior := ids[e.EvidenceID]; prior != "" {
			issues = append(issues, domain.ResourceIssue{Code: "duplicate_evidence_id", Index: &index, EvidenceID: e.EvidenceID, ExistingEvidenceID: prior, Message: "evidence_id 冲突"})
		} else {
			ids[e.EvidenceID] = e.EvidenceID
		}
		if prior := digests[e.ContentDigest]; prior != "" {
			issues = append(issues, domain.ResourceIssue{Code: "duplicate_content_digest", Index: &index, EvidenceID: e.EvidenceID, ExistingEvidenceID: prior, Message: "内容摘要重复"})
		} else {
			digests[e.ContentDigest] = e.EvidenceID
		}
		if e.EvidenceType == "rating_measurement" && e.WaterLevelM != nil && e.DischargeM3S != nil {
			k := fmt.Sprintf("%s/%.9f/%.9f", e.ObservedAt.UTC().Format(time.RFC3339Nano), *e.WaterLevelM, *e.DischargeM3S)
			if prior := measurements[k]; prior != "" {
				issues = append(issues, domain.ResourceIssue{Code: "duplicate_observation_combination", Index: &index, EvidenceID: e.EvidenceID, ExistingEvidenceID: prior, Message: "同一观测时刻水位流量组合重复"})
			} else {
				measurements[k] = e.EvidenceID
			}
		}
	}
	return out, issues
}

func (s *Service) AddEvidenceBatch(ctx context.Context, caseID string, in EvidenceBatchInput) (Result, error) {
	if err := validateMeta(in.Meta); err != nil {
		return Result{}, err
	}
	if len(in.Evidence) < 1 || len(in.Evidence) > 200 {
		return Result{}, fmt.Errorf("%w: evidence 数量必须为 1 至 200", domain.ErrInvalid)
	}
	station, err := s.store.CaseStation(ctx, caseID)
	if err != nil {
		return Result{}, err
	}
	lock := s.stationLock(station)
	lock.Lock()
	defer lock.Unlock()
	fingerprint := domain.Digest(in.Evidence)
	command := "evidence_batch_registered/" + fingerprint
	var out Result
	err = s.store.Within(ctx, func(tx *repository.Tx) error {
		stored, cmd, status, body, ok, err := tx.GetIdempotent(ctx, in.RequestID)
		if err != nil {
			return err
		}
		if ok {
			if stored != caseID || cmd != command {
				return fmt.Errorf("%w: request_id 已用于不同批次或案件", domain.ErrConflict)
			}
			out = Result{Status: status, Body: body, Replayed: true}
			return nil
		}
		c, err := tx.LoadCase(ctx, caseID)
		if err != nil {
			return err
		}
		if c.State != domain.Draft {
			return fmt.Errorf("%w: 仅 draft 案件可批量登记证据", domain.ErrGate)
		}
		if c.Revision != in.ExpectedRevision {
			return fmt.Errorf("%w: 当前 revision 为 %d", domain.ErrConflict, c.Revision)
		}
		items, issues := validateEvidenceBatch(c, in.Evidence)
		if len(issues) > 0 {
			return &domain.StructuredError{Kind: domain.ErrInvalid, Issues: issues}
		}
		previous := c.Revision
		now := s.now()
		ids := make([]string, 0, len(items))
		summaries := make([]map[string]any, 0, len(items))
		for _, e := range items {
			c.Evidence = append(c.Evidence, e)
			c.EvidenceVersions = append(c.EvidenceVersions, domain.EvidenceVersion{EvidenceID: e.EvidenceID, Version: 1, ContentDigest: e.ContentDigest, ActorID: in.ActorID, CreatedAt: now, Snapshot: e})
			ids = append(ids, e.EvidenceID)
			bindings := append([]domain.InstrumentBinding(nil), e.InstrumentBindings...)
			sort.Slice(bindings, func(i, j int) bool { return bindings[i].InstrumentKind < bindings[j].InstrumentKind })
			summaries = append(summaries, map[string]any{"evidence_id": e.EvidenceID, "content_digest": e.ContentDigest, "instrument_bindings": bindings})
		}
		sort.Strings(ids)
		sort.Slice(summaries, func(i, j int) bool {
			return summaries[i]["evidence_id"].(string) < summaries[j]["evidence_id"].(string)
		})
		batchDigest := domain.Digest(summaries)
		c.Revision++
		if err = c.ValidateConsistency(); err != nil {
			return err
		}
		events, err := tx.Events(ctx, caseID)
		if err != nil {
			return err
		}
		prev := ""
		if len(events) > 0 {
			prev = events[len(events)-1].EventDigest
		}
		payload := map[string]any{"batch_digest": batchDigest, "count": len(items), "items": summaries, "revision": c.Revision}
		event, err := audit.BuildEvent(caseID, int64(len(events)+1), "evidence_batch_registered", in.ActorID, in.RequestID, now, payload, prev)
		if err != nil {
			return err
		}
		if err = tx.AppendEvent(ctx, event); err != nil {
			return err
		}
		if err = tx.SaveCase(ctx, c, previous); err != nil {
			return err
		}
		response := EvidenceBatchResponse{CaseID: caseID, EvidenceIDs: ids, BatchDigest: batchDigest, Count: len(ids), Revision: c.Revision}
		body = marshal(response)
		if err = tx.SaveIdempotent(ctx, in.RequestID, caseID, command, 200, body); err != nil {
			return err
		}
		out = Result{Status: 200, Body: body}
		return nil
	})
	return out, err
}

type BaselinePreflight struct {
	CaseID                    string                 `json:"case_id"`
	Revision                  int64                  `json:"revision"`
	ProposedBaselineDigest    string                 `json:"proposed_baseline_digest"`
	EvidenceTypeCounts        map[string]int         `json:"evidence_type_counts"`
	PendingQualityEvidenceIDs []string               `json:"pending_quality_evidence_ids"`
	DuplicateEvidenceIDs      []string               `json:"duplicate_measurement_evidence_ids"`
	FreezeBlockingCodes       []string               `json:"freeze_blocking_codes"`
	DownstreamWarningCodes    []string               `json:"downstream_warning_codes"`
	Issues                    []domain.ResourceIssue `json:"issues"`
	Manifest                  []domain.BaselineItem  `json:"manifest"`
}
type BaselineConflictError struct {
	Latest BaselinePreflight `json:"latest_preflight"`
}

func (e *BaselineConflictError) Error() string {
	return fmt.Sprintf("%v: 基线预检摘要或 revision 已变化", domain.ErrConflict)
}
func (e *BaselineConflictError) Unwrap() error { return domain.ErrConflict }

func (s *Service) buildBaselinePreflight(c *domain.Case) BaselinePreflight {
	m := domain.BuildBaselineManifest(c, time.Time{})
	out := BaselinePreflight{CaseID: c.CaseID, Revision: c.Revision, ProposedBaselineDigest: m.Digest, EvidenceTypeCounts: map[string]int{}, PendingQualityEvidenceIDs: []string{}, DuplicateEvidenceIDs: []string{}, FreezeBlockingCodes: []string{}, DownstreamWarningCodes: []string{}, Issues: []domain.ResourceIssue{}, Manifest: m.Items}
	codes, warnings := map[string]bool{}, map[string]bool{}
	for _, e := range c.Evidence {
		out.EvidenceTypeCounts[e.EvidenceType]++
		if e.QualityDecision == "" || strings.HasPrefix(e.QualityDecision, "pending") {
			out.PendingQualityEvidenceIDs = append(out.PendingQualityEvidenceIDs, e.EvidenceID)
			warnings["quality_decision_pending"] = true
		}
	}
	for _, typ := range []string{"rating_measurement", "cross_section", "field_record"} {
		if out.EvidenceTypeCounts[typ] == 0 {
			code := "missing_required_evidence_" + typ
			codes[code] = true
			out.Issues = append(out.Issues, domain.ResourceIssue{Code: code, Message: "缺少必备证据类型 " + typ})
		}
	}
	for _, e := range c.Evidence {
		chain, err := c.EvidenceVersionChain(e.EvidenceID)
		if err != nil || len(chain) == 0 || chain[len(chain)-1].ContentDigest != e.ContentDigest {
			codes["evidence_version_chain_broken"] = true
			out.Issues = append(out.Issues, domain.ResourceIssue{Code: "evidence_version_chain_broken", EvidenceID: e.EvidenceID, Message: "证据版本链断裂"})
		}
	}
	quality := s.engine.EvaluateSamples(c.Evidence)
	for _, i := range quality.Issues {
		if i.Rule == "duplicate_time_level" || i.Rule == "duplicate_level_discharge" {
			out.DuplicateEvidenceIDs = append(out.DuplicateEvidenceIDs, i.EvidenceID)
			warnings["duplicate_measurement"] = true
		}
	}
	coverage := s.engine.CoverageMatrix(c.Evidence, c.Qualifications)
	for _, code := range coverage.BlockingCodes {
		warnings[code] = true
	}
	for _, cell := range coverage.Cells {
		if !cell.Covered {
			warnings[cell.IssueCode] = true
			codes["instrument_binding_or_coverage_failed"] = true
			qid := cell.QualificationID
			if qid == "" && len(cell.QualificationIDs) > 0 {
				qid = cell.QualificationIDs[0]
			}
			out.Issues = append(out.Issues, domain.ResourceIssue{Code: cell.IssueCode, EvidenceID: cell.EvidenceID, QualificationID: qid, InstrumentKind: cell.InstrumentKind, Message: "仪器覆盖未通过"})
		}
	}
	for code := range codes {
		out.FreezeBlockingCodes = append(out.FreezeBlockingCodes, code)
	}
	for code := range warnings {
		out.DownstreamWarningCodes = append(out.DownstreamWarningCodes, code)
	}
	sort.Strings(out.FreezeBlockingCodes)
	sort.Strings(out.DownstreamWarningCodes)
	sort.Strings(out.PendingQualityEvidenceIDs)
	sort.Strings(out.DuplicateEvidenceIDs)
	return out
}
func (s *Service) BaselinePreflight(ctx context.Context, caseID string) (BaselinePreflight, error) {
	c, err := s.store.LoadCase(ctx, caseID)
	if err != nil {
		return BaselinePreflight{}, err
	}
	if c.State != domain.Draft {
		return BaselinePreflight{}, fmt.Errorf("%w: 仅 draft 可冻结预检", domain.ErrGate)
	}
	return s.buildBaselinePreflight(c), nil
}

func (s *Service) ReplayAssessment(ctx context.Context, caseID, runID string) (assessment.ReplayVerification, error) {
	c, err := s.store.LoadCase(ctx, caseID)
	if err != nil {
		return assessment.ReplayVerification{}, err
	}
	if c.Assessment == nil || c.Assessment.RunID != runID {
		return assessment.ReplayVerification{}, domain.ErrNotFound
	}
	if c.State == domain.Archived {
		a, err := s.store.Archive(ctx, caseID)
		if err != nil {
			return assessment.ReplayVerification{}, err
		}
		found := false
		manifestKeys := map[string]bool{}
		for _, x := range a.Manifest {
			manifestKeys[x.Kind+"/"+x.ID] = true
			if x.Kind == "assessment_run" && x.ID == runID {
				found = true
			}
		}
		if !found {
			return assessment.ReplayVerification{UnreplayableReason: "run_not_in_archive_manifest"}, nil
		}
		if c.BaselineManifest == nil {
			return assessment.ReplayVerification{UnreplayableReason: "baseline_manifest_missing"}, nil
		}
		for _, x := range c.BaselineManifest.Items {
			kind := "evidence_version"
			if x.Kind == "qualification" {
				kind = "instrument_qualification_version"
			}
			key := fmt.Sprintf("%s/%s/v%d", kind, x.ID, x.Version)
			if !manifestKeys[key] {
				return assessment.ReplayVerification{UnreplayableReason: "baseline_input_not_in_archive_manifest"}, nil
			}
		}
	}
	return s.engine.Replay(c, c.Assessment)
}

type ReplacementInput struct {
	Meta
	Reason                string    `json:"reason"`
	EvidenceRef           string    `json:"evidence_ref"`
	ObservationID         string    `json:"observation_id"`
	ObservedAt            time.Time `json:"observed_at"`
	WaterLevelM           float64   `json:"water_level_m"`
	MeasuredDischargeM3S  float64   `json:"measured_discharge_m3s"`
	PredictedDischargeM3S float64   `json:"predicted_discharge_m3s"`
}

func (s *Service) ReplaceTrialObservation(ctx context.Context, caseID, oldID string, in ReplacementInput) (Result, error) {
	return s.mutateCommand(ctx, caseID, in.Meta, "trial_observation_replaced", "trial_observation_replaced/"+oldID+"/"+domain.Digest(in), func(c *domain.Case, _ *repository.Tx) error {
		if err := c.RequireState(domain.TrialActive, domain.TrialSuspended, domain.TrialQualified); err != nil {
			return err
		}
		if strings.TrimSpace(in.Reason) == "" || strings.TrimSpace(in.EvidenceRef) == "" || in.ObservationID == "" {
			return fmt.Errorf("%w: reason、evidence_ref 和新 observation_id 必填", domain.ErrInvalid)
		}
		idx := -1
		for i, x := range c.TrialObservations {
			if x.ObservationID == oldID {
				idx = i
			}
			if x.ObservationID == in.ObservationID {
				return fmt.Errorf("%w: observation_id 重复", domain.ErrConflict)
			}
		}
		if idx < 0 {
			return domain.ErrNotFound
		}
		old := c.TrialObservations[idx]
		if old.RecordState == "superseded" || old.SupersededBy != "" {
			return fmt.Errorf("%w: 原测次已被替代", domain.ErrConflict)
		}
		for _, x := range c.TrialObservations {
			if x.ObservationID != oldID && x.RecordState != "superseded" && x.ObservedAt.Equal(in.ObservedAt) && math.Abs(x.WaterLevelM-in.WaterLevelM) < 1e-9 {
				return fmt.Errorf("%w: observed_at 与 water_level_m 组合重复", domain.ErrConflict)
			}
		}
		if c.Assessment == nil || in.WaterLevelM < c.Assessment.LowerBoundM || in.WaterLevelM > c.Assessment.UpperBoundM {
			return fmt.Errorf("%w: 校核水位超出适用边界", domain.ErrGate)
		}
		for _, actor := range trialRestrictedActors(c) {
			if actor == in.ActorID {
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
			return fmt.Errorf("%w: 替代测次不在试用有效期内", domain.ErrGate)
		}
		if c.SettlementFor(trial.DecisionID) != nil {
			return fmt.Errorf("%w: 旧试用决定已经结算", domain.ErrGate)
		}
		next, err := s.engine.TrialObservation(in.ObservationID, in.ObservedAt, in.WaterLevelM, in.MeasuredDischargeM3S, in.PredictedDischargeM3S)
		if err != nil {
			return err
		}
		next.SubmittedBy = in.ActorID
		next.TrialDecisionID = trial.DecisionID
		next.Band = assessment.TrialBand(c.Assessment, next.WaterLevelM)
		next.CountsTowardProgress = true
		next.RecordState = "active"
		next.Supersedes = oldID
		c.TrialObservations[idx].RecordState = "superseded"
		c.TrialObservations[idx].SupersededBy = next.ObservationID
		c.TrialObservations[idx].SupersededReason = in.Reason
		c.TrialObservations[idx].ReplacementEvidenceRef = in.EvidenceRef
		c.TrialObservations[idx].CountsTowardProgress = false
		c.TrialObservations = append(c.TrialObservations, next)
		refreshTrialContributions(c)
		if c.State != domain.TrialSuspended {
			if next.Verdict == "suspend" {
				sid := "suspension-" + next.ObservationID
				c.TrialObservations[len(c.TrialObservations)-1].SuspensionID = sid
				c.TrialSuspensions = append(c.TrialSuspensions, domain.TrialSuspension{SuspensionID: sid, TriggerObservationID: next.ObservationID, ActualBias: math.Abs(next.RelativeBias), Threshold: s.engine.TrialBiasLimit, SuspendedAt: s.now(), RollbackRequired: true, State: "active", ObservationActorID: in.ActorID, RecoveryAttempts: []domain.RecoveryAttempt{}})
				c.State = domain.TrialSuspended
			} else {
				c.State = domain.TrialActive
			}
		}
		return nil
	})
}

type TimelineInput struct {
	EventType, ActorID, RequestID, Cursor string
	StartSequence, EndSequence            int64
	Limit                                 int
}
type TimelinePage struct {
	Events                 []domain.AuditEvent `json:"events"`
	NextCursor             string              `json:"next_cursor,omitempty"`
	PreviousDigest         string              `json:"previous_digest"`
	LastEventDigest        string              `json:"last_event_digest"`
	ChainHeadDigest        string              `json:"chain_head_digest"`
	TotalEvents            int                 `json:"total_events"`
	PageIntegrity          bool                `json:"page_integrity"`
	ResponseStatusDigest   string              `json:"response_status_digest,omitempty"`
	ArchiveDigest          string              `json:"archive_digest,omitempty"`
	ArchiveTerminalMatched *bool               `json:"archive_terminal_matched,omitempty"`
}

func (s *Service) TimelinePage(ctx context.Context, caseID string, in TimelineInput) (TimelinePage, error) {
	c, err := s.store.LoadCase(ctx, caseID)
	if err != nil {
		return TimelinePage{}, err
	}
	if in.Limit == 0 {
		in.Limit = 50
	}
	if in.Limit < 1 || in.Limit > 200 || in.StartSequence < 0 || in.EndSequence < 0 || (in.EndSequence > 0 && in.StartSequence > in.EndSequence) {
		return TimelinePage{}, fmt.Errorf("%w: 时间线区间或 limit 无效", domain.ErrInvalid)
	}
	events, err := s.store.Events(ctx, caseID)
	if err != nil {
		return TimelinePage{}, err
	}
	if ok, pos, msg := audit.Verify(events); !ok {
		return TimelinePage{}, fmt.Errorf("%w: audit_chain position=%d %s", domain.ErrGate, pos, msg)
	}
	if in.EventType != "" && !domain.AuditEventTypes[in.EventType] {
		return TimelinePage{}, fmt.Errorf("%w: event_type 未知", domain.ErrInvalid)
	}
	after := int64(0)
	if in.Cursor != "" {
		raw, e := base64.RawURLEncoding.DecodeString(in.Cursor)
		if e != nil {
			return TimelinePage{}, fmt.Errorf("%w: cursor 无效", domain.ErrInvalid)
		}
		after, e = strconv.ParseInt(string(raw), 10, 64)
		if e != nil {
			return TimelinePage{}, fmt.Errorf("%w: cursor 无效", domain.ErrInvalid)
		}
	}
	// Reuse the events backing array for event-type filtering to avoid an extra
	// allocation. Store.Events may provide a cached slice, so this also becomes
	// the mutation target for subsequent timeline requests.
	matched := []domain.AuditEvent{}
	if in.EventType != "" {
		matched = events[:0]
	}
	for _, e := range events {
		if e.SequenceNo <= after || (in.StartSequence > 0 && e.SequenceNo < in.StartSequence) || (in.EndSequence > 0 && e.SequenceNo > in.EndSequence) || (in.EventType != "" && e.EventType != in.EventType) || (in.ActorID != "" && e.ActorID != in.ActorID) || (in.RequestID != "" && e.RequestID != in.RequestID) {
			continue
		}
		matched = append(matched, e)
	}
	if in.RequestID != "" && len(matched) == 0 {
		return TimelinePage{}, domain.ErrNotFound
	}
	more := len(matched) > in.Limit
	if more {
		matched = matched[:in.Limit]
	}
	out := TimelinePage{Events: matched, TotalEvents: len(events), PageIntegrity: true}
	if len(events) > 0 {
		out.ChainHeadDigest = events[len(events)-1].EventDigest
	}
	if len(matched) > 0 {
		first, last := matched[0], matched[len(matched)-1]
		out.PreviousDigest = first.PreviousDigest
		out.LastEventDigest = last.EventDigest
		if more {
			out.NextCursor = base64.RawURLEncoding.EncodeToString([]byte(strconv.FormatInt(last.SequenceNo, 10)))
		}
	}
	if in.RequestID != "" {
		status, response, err := s.store.IdempotentStatus(ctx, caseID, in.RequestID)
		if err != nil {
			return TimelinePage{}, err
		}
		out.ResponseStatusDigest = domain.Digest(struct {
			Status         int    `json:"status"`
			ResponseDigest string `json:"response_digest"`
		}{status, domain.Digest(response)})
	}
	if c.State == domain.Archived {
		a, e := s.store.Archive(ctx, caseID)
		if e != nil {
			return TimelinePage{}, e
		}
		out.ArchiveDigest = a.Digest
		matchedTerminal := len(a.Events) > 0 && a.Events[len(a.Events)-1].EventDigest == out.ChainHeadDigest
		out.ArchiveTerminalMatched = &matchedTerminal
	}
	return out, nil
}
