package application

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"

	"benzhi-project-cf63ae2b-9e6c-4d6e-b63f-f6a510693519/internal/audit"
	"benzhi-project-cf63ae2b-9e6c-4d6e-b63f-f6a510693519/internal/domain"
	"benzhi-project-cf63ae2b-9e6c-4d6e-b63f-f6a510693519/internal/repository"
)

type InstrumentInvalidationInput struct {
	Meta
	InvalidationType          string    `json:"invalidation_type"`
	InvalidatedAt             time.Time `json:"invalidated_at"`
	Reason                    string    `json:"reason"`
	NotificationEvidenceRef   string    `json:"notification_evidence_ref"`
	OriginalCertificateDigest string    `json:"original_certificate_digest"`
}

type InstrumentInvalidationResponse struct {
	CaseID           string                        `json:"case_id"`
	Revision         int64                         `json:"revision"`
	State            domain.State                  `json:"state"`
	Invalidation     domain.InstrumentInvalidation `json:"invalidation"`
	AffectedBindings []domain.InstrumentImpact     `json:"affected_bindings"`
	NextAction       string                        `json:"next_action"`
}

func (s *Service) InvalidateInstrument(ctx context.Context, caseID, qualificationID string, in InstrumentInvalidationInput) (Result, error) {
	if err := validateMeta(in.Meta); err != nil {
		return Result{}, err
	}
	if strings.TrimSpace(caseID) == "" || strings.TrimSpace(qualificationID) == "" {
		return Result{}, fmt.Errorf("%w: case_id 和 qualification_id 必填", domain.ErrInvalid)
	}
	stationID, err := s.store.CaseStation(ctx, caseID)
	if err != nil {
		return Result{}, err
	}
	lock := s.stationLock(stationID)
	lock.Lock()
	defer lock.Unlock()
	command := "instrument_certificate_invalidated/" + qualificationID
	var out Result
	err = s.store.Within(ctx, func(tx *repository.Tx) error {
		storedCaseID, storedCommand, status, body, ok, err := tx.GetIdempotent(ctx, in.RequestID)
		if err != nil {
			return err
		}
		if ok {
			if storedCaseID != caseID || storedCommand != command {
				return fmt.Errorf("%w: request_id 已用于不同命令", domain.ErrConflict)
			}
			out = Result{Status: status, Body: body, Replayed: true}
			return nil
		}
		c, err := tx.LoadCase(ctx, caseID)
		if err != nil {
			return err
		}
		if c.Revision != in.ExpectedRevision {
			return fmt.Errorf("%w: 当前 revision 为 %d", domain.ErrConflict, c.Revision)
		}
		q, err := c.FrozenQualification(qualificationID)
		if err != nil {
			return err
		}
		if err = c.ValidateInstrumentInvalidation(q, in.InvalidationType, in.InvalidatedAt, in.Reason, in.NotificationEvidenceRef, in.OriginalCertificateDigest); err != nil {
			return err
		}
		impacts, err := s.engine.InvalidatedBindings(c, qualificationID, in.InvalidationType)
		if err != nil {
			return err
		}
		previous := c.Revision
		now := s.now()
		record := domain.InstrumentInvalidation{InvalidationID: "invalidation-" + qualificationID, QualificationID: qualificationID, InvalidationType: in.InvalidationType, InvalidatedAt: domain.NormalizedTime(in.InvalidatedAt), Reason: in.Reason, NotificationEvidenceRef: in.NotificationEvidenceRef, OriginalCertificateDigest: in.OriginalCertificateDigest, AffectedBindings: impacts, ReportedBy: in.ActorID, ReportedAt: now}
		nextAction := "none"
		if len(impacts) > 0 {
			record.DeviationID = "instrument-invalidation-" + qualificationID
			deviation := domain.Deviation{DeviationID: record.DeviationID, SourceGate: "instrument_certificate_invalidated", Severity: "major", State: "open"}
			domain.InitializeDeviation(&deviation, in.ActorID, now)
			deviation.PhaseHistory[0].Description = "校准证书失效通报自动建立"
			c.Deviations = append(c.Deviations, deviation)
			for i := range c.ReviewRounds {
				round := &c.ReviewRounds[i]
				if round.Decision == "pass" && !round.Invalidated {
					round.Invalidated, round.InvalidatedAt, round.InvalidationReason = true, &now, record.DeviationID
				}
			}
			for i := range c.Decisions {
				decision := &c.Decisions[i]
				if decision.DecisionType == "trial" && decision.Status != "invalidated" && decision.Status != "expired_unqualified" {
					decision.Status, decision.InvalidatedAt, decision.InvalidationReason = "invalidated", &now, record.DeviationID
				}
			}
			if err = tx.ReleaseTrial(ctx, c.StationID, c.CaseID); err != nil {
				return err
			}
			c.State = domain.Assessed
			nextAction = "deviation_remediation"
		}
		c.InstrumentInvalidations = append(c.InstrumentInvalidations, record)
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
		payload := map[string]any{"qualification_id": qualificationID, "original_certificate_digest": q.Digest, "invalidation_type": in.InvalidationType, "invalidated_at": record.InvalidatedAt, "affected_bindings": impacts, "deviation_id": record.DeviationID, "state": c.State, "revision": c.Revision}
		event, err := audit.BuildEvent(caseID, int64(len(events)+1), "instrument_certificate_invalidated", in.ActorID, in.RequestID, now, payload, prevDigest)
		if err != nil {
			return err
		}
		if err = tx.AppendEvent(ctx, event); err != nil {
			return err
		}
		if err = tx.SaveCase(ctx, c, previous); err != nil {
			return err
		}
		response := InstrumentInvalidationResponse{CaseID: caseID, Revision: c.Revision, State: c.State, Invalidation: record, AffectedBindings: impacts, NextAction: nextAction}
		body = marshal(response)
		if err = tx.SaveIdempotent(ctx, in.RequestID, caseID, command, 200, body); err != nil {
			return err
		}
		out = Result{Status: 200, Body: body}
		return nil
	})
	return out, err
}

type TrialExpirySettlementResponse struct {
	CaseID         string                        `json:"case_id"`
	Revision       int64                         `json:"revision"`
	State          domain.State                  `json:"state"`
	Outcome        string                        `json:"outcome"`
	Settlement     *domain.TrialExpirySettlement `json:"settlement,omitempty"`
	EffectiveUntil time.Time                     `json:"effective_until"`
	NextAction     string                        `json:"next_action"`
}

func (s *Service) SettleTrialExpiry(ctx context.Context, caseID string, meta Meta) (Result, error) {
	if err := validateMeta(meta); err != nil {
		return Result{}, err
	}
	stationID, err := s.store.CaseStation(ctx, caseID)
	if err != nil {
		return Result{}, err
	}
	lock := s.stationLock(stationID)
	lock.Lock()
	defer lock.Unlock()
	const command = "trial_expiry_settled"
	var out Result
	err = s.store.Within(ctx, func(tx *repository.Tx) error {
		storedCaseID, storedCommand, status, body, ok, err := tx.GetIdempotent(ctx, meta.RequestID)
		if err != nil {
			return err
		}
		if ok {
			if storedCaseID != caseID || storedCommand != command {
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
			return fmt.Errorf("%w: 当前 revision 为 %d", domain.ErrConflict, c.Revision)
		}
		if err = c.RequireState(domain.TrialActive, domain.TrialSuspended); err != nil {
			return err
		}
		trial, err := c.LatestOpenTrial()
		if err != nil {
			return err
		}
		now := s.now()
		if now.Before(*trial.EffectiveUntil) {
			body = marshal(TrialExpirySettlementResponse{CaseID: caseID, Revision: c.Revision, State: c.State, Outcome: "trial_not_expired", EffectiveUntil: *trial.EffectiveUntil, NextAction: "wait_for_expiry"})
			if err = tx.SaveIdempotent(ctx, meta.RequestID, caseID, command, 200, body); err != nil {
				return err
			}
			out = Result{Status: 200, Body: body}
			return nil
		}
		if c.SettlementFor(trial.DecisionID) != nil {
			return fmt.Errorf("%w: trial_decision_already_settled", domain.ErrConflict)
		}
		progress := s.engine.TrialProgressAtExpiry(c, *trial)
		active := []string{}
		for _, suspension := range c.TrialSuspensions {
			if suspension.State == "active" {
				active = append(active, suspension.SuspensionID)
			}
		}
		sort.Strings(active)
		unmet := append([]string(nil), progress.UnmetGates...)
		if len(active) > 0 {
			unmet = append(unmet, "active_trial_suspension")
		}
		sort.Strings(unmet)
		verdict, nextAction := "qualified", "activation"
		if len(unmet) == 0 {
			trial.Status = "qualified"
			c.State = domain.TrialQualified
		} else {
			verdict, nextAction = "expired_unqualified", "review"
			trial.Status = verdict
			for i := range c.TrialSuspensions {
				if c.TrialSuspensions[i].State == "active" {
					c.TrialSuspensions[i].State = "terminated_at_expiry"
				}
			}
			if err = tx.ReleaseTrial(ctx, c.StationID, c.CaseID); err != nil {
				return err
			}
			c.State = domain.DeviationsClosed
		}
		settlement := domain.TrialExpirySettlement{SettlementID: "settlement-" + trial.DecisionID, DecisionID: trial.DecisionID, EffectiveUntil: *trial.EffectiveUntil, SettledAt: now, Verdict: verdict, Progress: progress, UnmetGates: unmet, ActiveSuspensions: active, NextAction: nextAction}
		c.TrialExpirySettlements = append(c.TrialExpirySettlements, settlement)
		previous := c.Revision
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
		event, err := audit.BuildEvent(caseID, int64(len(events)+1), "trial_expiry_settled", meta.ActorID, meta.RequestID, now, settlement, prevDigest)
		if err != nil {
			return err
		}
		if err = tx.AppendEvent(ctx, event); err != nil {
			return err
		}
		if err = tx.SaveCase(ctx, c, previous); err != nil {
			return err
		}
		response := TrialExpirySettlementResponse{CaseID: caseID, Revision: c.Revision, State: c.State, Outcome: verdict, Settlement: &settlement, EffectiveUntil: *trial.EffectiveUntil, NextAction: nextAction}
		body = marshal(response)
		if err = tx.SaveIdempotent(ctx, meta.RequestID, caseID, command, 200, body); err != nil {
			return err
		}
		out = Result{Status: 200, Body: body}
		return nil
	})
	return out, err
}

type ContinuityIssue struct {
	Code    string `json:"code"`
	Path    string `json:"path"`
	Message string `json:"message"`
}

type ContinuityVersionResult struct {
	CaseID          string            `json:"case_id"`
	CurveVersion    string            `json:"curve_version"`
	EffectiveFrom   string            `json:"effective_from"`
	EffectiveUntil  string            `json:"effective_until,omitempty"`
	DecisionID      string            `json:"decision_id"`
	ReplacesVersion string            `json:"replaces_version"`
	ArchiveDigest   string            `json:"archive_digest,omitempty"`
	ArchiveVerdict  string            `json:"archive_verdict"`
	EventChainHead  string            `json:"event_chain_head,omitempty"`
	EventChainTail  string            `json:"event_chain_tail,omitempty"`
	Issues          []ContinuityIssue `json:"issues"`
}

type CertificationContinuity struct {
	StationID       string                    `json:"station_id"`
	OverallVerdict  string                    `json:"overall_verdict"`
	Versions        []ContinuityVersionResult `json:"versions"`
	ContinuousFrom  string                    `json:"continuous_from,omitempty"`
	ContinuousUntil string                    `json:"continuous_until,omitempty"`
	ChainHead       string                    `json:"chain_head,omitempty"`
	ChainTail       string                    `json:"chain_tail,omitempty"`
	CurrentVersion  string                    `json:"current_version,omitempty"`
	PointerVersion  string                    `json:"pointer_version,omitempty"`
	Issues          []ContinuityIssue         `json:"issues"`
}

func (s *Service) CertificationContinuity(ctx context.Context, stationID string) (CertificationContinuity, error) {
	if strings.TrimSpace(stationID) == "" || len(stationID) > 80 {
		return CertificationContinuity{}, fmt.Errorf("%w: station_id 必填且不超过 80 字符", domain.ErrInvalid)
	}
	history, err := s.store.CurveHistory(ctx, stationID)
	if err != nil {
		return CertificationContinuity{}, err
	}
	if len(history) == 0 {
		return CertificationContinuity{}, domain.ErrNotFound
	}
	out := CertificationContinuity{StationID: stationID, OverallVerdict: "pass", Versions: []ContinuityVersionResult{}, Issues: []ContinuityIssue{}}
	add := func(issue ContinuityIssue) { out.Issues = append(out.Issues, issue); out.OverallVerdict = "fail" }
	seenVersions := map[string]bool{}
	for i, item := range history {
		path := fmt.Sprintf("versions[%d]", i)
		version := stringValue(item, "curve_version")
		from := stringValue(item, "effective_from")
		until := stringValue(item, "effective_until")
		result := ContinuityVersionResult{CaseID: stringValue(item, "case_id"), CurveVersion: version, EffectiveFrom: from, EffectiveUntil: until, DecisionID: stringValue(item, "decision_id"), ReplacesVersion: stringValue(item, "replaces_version"), ArchiveVerdict: "fail", Issues: []ContinuityIssue{}}
		versionIssue := func(code, subpath, message string) {
			issue := ContinuityIssue{Code: code, Path: path + subpath, Message: message}
			result.Issues = append(result.Issues, issue)
			add(issue)
		}
		if seenVersions[version] {
			versionIssue("duplicate_curve_version", ".curve_version", "测站履历包含重复版本")
		}
		seenVersions[version] = true
		fromTime, fromErr := time.Parse(time.RFC3339Nano, from)
		if fromErr != nil {
			versionIssue("effective_interval_invalid", ".effective_from", "effective_from 不是有效 RFC3339 时刻")
		}
		if until != "" {
			untilTime, untilErr := time.Parse(time.RFC3339Nano, until)
			if untilErr != nil {
				versionIssue("effective_interval_invalid", ".effective_until", "effective_until 不是有效 RFC3339 时刻")
			} else if fromErr == nil && !untilTime.After(fromTime) {
				versionIssue("effective_interval_inverted", ".effective_until", "履历结束时刻不晚于开始时刻")
			}
		}
		if i == 0 {
			out.ContinuousFrom = from
			if result.ReplacesVersion != "" {
				versionIssue("replacement_chain_broken", ".replaces_version", "首版本不得声明前任")
			}
		} else {
			previous := history[i-1]
			if result.ReplacesVersion != stringValue(previous, "curve_version") {
				versionIssue("replacement_chain_broken", ".replaces_version", "replaces_version 未指向上一履历版本")
			}
			previousUntil := stringValue(previous, "effective_until")
			if previousUntil != from {
				code := "effective_interval_gap"
				if previousUntil != "" && previousUntil > from {
					code = "effective_interval_overlap"
				}
				versionIssue(code, ".effective_from", "相邻正式曲线交接时刻不连续")
			}
		}
		c, caseErr := s.store.LoadCase(ctx, result.CaseID)
		if caseErr != nil {
			versionIssue("dangling_case_id", ".case_id", "履历关联案件不存在")
		} else {
			var decision *domain.Decision
			for j := range c.Decisions {
				if c.Decisions[j].DecisionType == "activation" && c.Decisions[j].DecisionID == result.DecisionID {
					decision = &c.Decisions[j]
				}
			}
			if decision == nil {
				versionIssue("activation_decision_mismatch", ".decision_id", "案件缺少对应正式启用决定")
			} else {
				if decision.CurveVersion != version || decision.ReplacesVersion != result.ReplacesVersion || decision.EffectiveFrom.UTC().Format(time.RFC3339Nano) != from || c.CandidateVersion != version {
					versionIssue("activation_decision_mismatch", ".decision", "候选版本、替代关系或生效时刻与决定不一致")
				}
			}
			a, archiveErr := s.store.Archive(ctx, result.CaseID)
			if archiveErr != nil {
				versionIssue("archive_missing", ".archive", "正式曲线缺少只读认证档案")
			} else {
				result.ArchiveDigest = a.Digest
				ok, message := audit.VerifyArchive(a)
				if !ok {
					versionIssue("archive_integrity_failed", ".archive.archive_digest", message)
				} else {
					result.ArchiveVerdict = "pass"
				}
				if len(a.Events) > 0 {
					result.EventChainHead = a.Events[0].EventDigest
					result.EventChainTail = a.Events[len(a.Events)-1].EventDigest
				}
				member := false
				memberDigest := ""
				for _, manifest := range a.Manifest {
					if manifest.Kind == "decision" && manifest.ID == result.DecisionID {
						member = true
						memberDigest = manifest.Digest
					}
				}
				if !member {
					versionIssue("archive_decision_missing", ".archive.manifest", "正式启用决定不在档案清单中")
				} else if decision != nil && memberDigest != domain.Digest(*decision) {
					versionIssue("archive_decision_mismatch", ".archive.manifest", "档案决定摘要与适用边界或替代关系不一致")
				}
				archivedDecisionMatched := false
				for _, archivedDecision := range a.Case.Decisions {
					if archivedDecision.DecisionID == result.DecisionID && decision != nil && domain.Digest(archivedDecision) == domain.Digest(*decision) {
						archivedDecisionMatched = true
					}
				}
				if decision != nil && !archivedDecisionMatched {
					versionIssue("archive_decision_mismatch", ".archive.case.activation_decisions", "档案案件快照中的正式决定不一致")
				}
			}
		}
		out.Versions = append(out.Versions, result)
	}
	last := history[len(history)-1]
	out.ContinuousUntil = stringValue(last, "effective_until")
	if len(out.Versions) > 0 {
		out.ChainHead = out.Versions[0].EventChainHead
		out.ChainTail = out.Versions[len(out.Versions)-1].EventChainTail
	}
	pointer, err := s.store.CurvePointer(ctx, stationID)
	if err != nil {
		return CertificationContinuity{}, err
	}
	out.PointerVersion = pointer["curve_version"]
	if out.PointerVersion != stringValue(last, "curve_version") {
		add(ContinuityIssue{Code: "current_pointer_not_terminal", Path: "station_curves.curve_version", Message: "当前指针未指向履历末端版本"})
	}
	current, currentErr := s.store.CurveAsOf(ctx, stationID, s.now())
	if currentErr != nil {
		add(ContinuityIssue{Code: "no_current_version", Path: "current_curve", Message: "当前时刻没有命中的正式曲线"})
	} else {
		out.CurrentVersion = stringValue(current, "curve_version")
		if out.PointerVersion != out.CurrentVersion {
			add(ContinuityIssue{Code: "current_pointer_time_mismatch", Path: "station_curves.curve_version", Message: "测站指针与当前时刻命中版本不一致"})
		}
		currentQuery, queryErr := s.store.CurrentCurve(ctx, stationID)
		if queryErr != nil || currentQuery["curve_version"] != out.CurrentVersion {
			add(ContinuityIssue{Code: "current_curve_query_mismatch", Path: "current_curve.curve_version", Message: "current-curve 查询与履历时刻命中不一致"})
		}
	}
	sort.Slice(out.Issues, func(i, j int) bool {
		if out.Issues[i].Path == out.Issues[j].Path {
			return out.Issues[i].Code < out.Issues[j].Code
		}
		return out.Issues[i].Path < out.Issues[j].Path
	})
	return out, nil
}

func stringValue(value map[string]any, key string) string {
	if text, ok := value[key].(string); ok {
		return text
	}
	return ""
}
