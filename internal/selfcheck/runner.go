package selfcheck

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"
)

type Runner struct {
	BaseURL string
	Client  *http.Client
}
type caseResponse struct {
	CaseID   string `json:"case_id"`
	State    string `json:"state"`
	Revision int64  `json:"revision"`
	Evidence []struct {
		EvidenceID    string `json:"evidence_id"`
		ContentDigest string `json:"content_digest"`
	} `json:"evidence"`
	Qualifications []struct {
		QualificationID string `json:"qualification_id"`
		Digest          string `json:"digest"`
	} `json:"instrument_qualifications"`
}

func New(baseURL string) *Runner {
	return &Runner{BaseURL: baseURL, Client: &http.Client{Timeout: 3 * time.Second}}
}
func (r *Runner) request(ctx context.Context, method, path string, payload any, want int, out any) (http.Header, error) {
	var body io.Reader
	if payload != nil {
		raw, err := json.Marshal(payload)
		if err != nil {
			return nil, err
		}
		body = bytes.NewReader(raw)
	}
	req, err := http.NewRequestWithContext(ctx, method, r.BaseURL+path, body)
	if err != nil {
		return nil, err
	}
	if payload != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	req.Header.Set("X-Correlation-ID", "self-check")
	resp, err := r.Client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 2<<20))
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != want {
		return resp.Header, fmt.Errorf("%s %s: 期望状态 %d，实际 %d: %s", method, path, want, resp.StatusCode, string(raw))
	}
	if out != nil && len(raw) > 0 {
		if err = json.Unmarshal(raw, out); err != nil {
			return resp.Header, fmt.Errorf("解码 %s 响应失败: %w", path, err)
		}
	}
	return resp.Header, nil
}
func meta(request, actor string, revision int64) map[string]any {
	return map[string]any{"request_id": request, "actor_id": actor, "expected_revision": revision}
}
func merge(base map[string]any, extra map[string]any) map[string]any {
	for k, v := range extra {
		base[k] = v
	}
	return base
}

func (r *Runner) Run(parent context.Context) error {
	ctx, cancel := context.WithTimeout(parent, 20*time.Second)
	defer cancel()
	var health map[string]string
	deadline := time.Now().Add(3 * time.Second)
	for {
		if _, err := r.request(ctx, http.MethodGet, "/healthz", nil, 200, &health); err == nil {
			break
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("健康检查超时")
		}
		time.Sleep(20 * time.Millisecond)
	}
	created := map[string]any{"request_id": "req-create", "actor_id": "owner-a", "expected_revision": 0, "case_id": "case-self-check", "station_id": "station-self-check", "river_reach": "示范河段 K0+000 至 K1+500", "candidate_version": "curve-2026-flood-v1", "owner_id": "owner-a"}
	var c caseResponse
	if _, err := r.request(ctx, http.MethodPost, "/api/v1/curve-cases", created, 201, &c); err != nil {
		return err
	}
	if c.State != "draft" || c.Revision != 1 {
		return fmt.Errorf("建档结果不符合预期")
	}
	// Idempotency is checked before any revision comparison and must reproduce the original response.
	var replay caseResponse
	headers, err := r.request(ctx, http.MethodPost, "/api/v1/curve-cases", created, 201, &replay)
	if err != nil {
		return err
	}
	if headers.Get("Idempotent-Replayed") != "true" || replay.Revision != 1 {
		return fmt.Errorf("建档幂等重放验证失败")
	}
	duplicate := map[string]any{"request_id": "req-create-duplicate", "actor_id": "owner-a", "expected_revision": 0, "case_id": "case-self-check-duplicate", "station_id": "station-self-check", "river_reach": "示范河段", "candidate_version": "curve-2026-flood-v1", "owner_id": "owner-a"}
	if _, err = r.request(ctx, http.MethodPost, "/api/v1/curve-cases", duplicate, 409, nil); err != nil {
		return fmt.Errorf("重复候选拦截失败: %w", err)
	}
	var casePage struct {
		Items []caseResponse `json:"items"`
	}
	if _, err = r.request(ctx, http.MethodGet, "/api/v1/curve-cases?station_id=station-self-check&archived=false", nil, 200, &casePage); err != nil || len(casePage.Items) != 1 {
		return fmt.Errorf("案件集合查询失败: %v", err)
	}
	add := func(path, request, actor string, payload map[string]any) error {
		payload = merge(meta(request, actor, c.Revision), payload)
		if _, err := r.request(ctx, http.MethodPost, path, payload, 200, &c); err != nil {
			return err
		}
		return nil
	}
	basePath := "/api/v1/curve-cases/" + c.CaseID
	measurements := []map[string]any{
		{"evidence_id": "m-01", "evidence_type": "rating_measurement", "observed_at": "2026-07-01T01:00:00Z", "water_level_m": 1.0, "discharge_m3s": 10.0, "source_ref": "field/m-01", "content": "测次原始记录 01", "quality_decision": "included", "decision_reason": "范围和时序检查通过"},
		{"evidence_id": "m-02", "evidence_type": "rating_measurement", "observed_at": "2026-07-01T02:00:00Z", "water_level_m": 2.0, "discharge_m3s": 20.0, "source_ref": "field/m-02", "content": "测次原始记录 02", "quality_decision": "included", "decision_reason": "范围和时序检查通过"},
		{"evidence_id": "m-03", "evidence_type": "rating_measurement", "observed_at": "2026-07-01T03:00:00Z", "water_level_m": 3.0, "discharge_m3s": 30.0, "source_ref": "field/m-03", "content": "测次原始记录 03", "quality_decision": "included", "decision_reason": "范围和时序检查通过"},
		{"evidence_id": "m-04", "evidence_type": "rating_measurement", "observed_at": "2026-07-01T04:00:00Z", "water_level_m": 2.5, "discharge_m3s": 25.0, "source_ref": "field/m-04", "content": "测次原始记录 04", "quality_decision": "included", "decision_reason": "范围和时序检查通过"},
		{"evidence_id": "x-01", "evidence_type": "cross_section", "observed_at": "2026-06-30T10:00:00Z", "source_ref": "survey/x-01", "content": "断面测量坐标集", "quality_decision": "included", "decision_reason": "闭合差合格"},
		{"evidence_id": "f-01", "evidence_type": "field_record", "observed_at": "2026-07-01T01:00:00Z", "source_ref": "field/log-01", "content": "洪水期现场记录", "quality_decision": "included", "decision_reason": "签名完整"},
	}
	for i, item := range measurements {
		if err = add(basePath+"/evidence", fmt.Sprintf("req-evidence-%d", i), "analyst-a", item); err != nil {
			return err
		}
	}
	oldDigest := ""
	for _, e := range c.Evidence {
		if e.EvidenceID == "m-01" {
			oldDigest = e.ContentDigest
		}
	}
	if oldDigest == "" {
		return fmt.Errorf("证据摘要未返回")
	}
	if err = add(basePath+"/evidence/m-01/corrections", "req-evidence-correct", "analyst-a", map[string]any{"content_digest": oldDigest, "correction_reason": "修正原始记录转录尾数", "evidence_type": "rating_measurement", "observed_at": "2026-07-01T01:00:00Z", "water_level_m": 1.0, "discharge_m3s": 10.1, "source_ref": "field/m-01-corrected", "content": "测次原始记录 01（复核更正版）", "quality_decision": "included", "decision_reason": "范围和时序检查通过"}); err != nil {
		return err
	}
	var versions struct {
		Versions []any `json:"versions"`
	}
	if _, err = r.request(ctx, http.MethodGet, basePath+"/evidence/m-01/versions", nil, 200, &versions); err != nil || len(versions.Versions) != 2 {
		return fmt.Errorf("证据版本链检查失败: %v", err)
	}
	if err = add(basePath+"/evidence/quality-rejudgments", "req-quality-batch", "quality-a", map[string]any{"decisions": []map[string]any{{"evidence_id": "m-01", "decision": "included", "reason": "复裁通过"}, {"evidence_id": "m-02", "decision": "included", "reason": "复裁通过"}, {"evidence_id": "m-03", "decision": "included", "reason": "复裁通过"}, {"evidence_id": "m-04", "decision": "included", "reason": "复裁通过"}}}); err != nil {
		return err
	}
	instruments := []map[string]any{
		{"qualification_id": "q-current", "instrument_id": "current-101", "instrument_kind": "current_meter", "certificate_ref": "cert-current-2026", "calibrated_at": "2026-01-01T00:00:00Z", "valid_until": "2027-01-01T00:00:00Z", "usage_started_at": "2026-06-30T00:00:00Z", "usage_ended_at": "2026-07-02T00:00:00Z"},
		{"qualification_id": "q-level", "instrument_id": "level-202", "instrument_kind": "water_level_gauge", "certificate_ref": "cert-level-2026", "calibrated_at": "2026-01-01T00:00:00Z", "valid_until": "2027-01-01T00:00:00Z", "usage_started_at": "2026-06-30T00:00:00Z", "usage_ended_at": "2026-07-02T00:00:00Z"},
		{"qualification_id": "q-survey", "instrument_id": "survey-303", "instrument_kind": "survey_equipment", "certificate_ref": "cert-survey-2026", "calibrated_at": "2026-01-01T00:00:00Z", "valid_until": "2027-01-01T00:00:00Z", "usage_started_at": "2026-06-30T00:00:00Z", "usage_ended_at": "2026-07-02T00:00:00Z"},
	}
	for i, item := range instruments {
		if err = add(basePath+"/instrument-qualifications", fmt.Sprintf("req-instrument-%d", i), "quality-a", item); err != nil {
			return err
		}
	}
	oldQualificationDigest := ""
	for _, q := range c.Qualifications {
		if q.QualificationID == "q-current" {
			oldQualificationDigest = q.Digest
		}
	}
	if oldQualificationDigest == "" {
		return fmt.Errorf("仪器资格摘要未返回")
	}
	if err = add(basePath+"/instrument-qualifications/q-current/corrections", "req-instrument-correct", "quality-a", map[string]any{"previous_digest": oldQualificationDigest, "correction_reason": "更正证书引用转录", "instrument_id": "current-101", "instrument_kind": "current_meter", "certificate_ref": "cert-current-2026-corrected", "calibrated_at": "2026-01-01T00:00:00Z", "valid_until": "2027-01-01T00:00:00Z", "usage_started_at": "2026-06-30T00:00:00Z", "usage_ended_at": "2026-07-02T00:00:00Z"}); err != nil {
		return err
	}
	var qualificationVersions struct {
		Versions []any `json:"versions"`
	}
	if _, err = r.request(ctx, http.MethodGet, basePath+"/instrument-qualifications/q-current/versions", nil, 200, &qualificationVersions); err != nil || len(qualificationVersions.Versions) != 2 {
		return fmt.Errorf("仪器资格版本链检查失败: %v", err)
	}
	qualificationDigests := map[string]string{}
	for _, q := range c.Qualifications {
		qualificationDigests[q.QualificationID] = q.Digest
	}
	bind := func(id, request string, payload map[string]any, bindings []map[string]any) error {
		current := ""
		for _, e := range c.Evidence {
			if e.EvidenceID == id {
				current = e.ContentDigest
			}
		}
		payload["content_digest"], payload["correction_reason"], payload["instrument_bindings"] = current, "补充实际使用仪器和证书摘要", bindings
		return add(basePath+"/evidence/"+id+"/corrections", request, "analyst-a", payload)
	}
	measurementBindings := []map[string]any{{"instrument_kind": "current_meter", "instrument_id": "current-101", "qualification_id": "q-current", "certificate_digest": qualificationDigests["q-current"]}, {"instrument_kind": "water_level_gauge", "instrument_id": "level-202", "qualification_id": "q-level", "certificate_digest": qualificationDigests["q-level"]}}
	boundEvidence := []struct {
		id, request string
		payload     map[string]any
		bindings    []map[string]any
	}{
		{"m-01", "req-bind-m01", map[string]any{"evidence_type": "rating_measurement", "observed_at": "2026-07-01T01:00:00Z", "water_level_m": 1.0, "discharge_m3s": 10.1, "source_ref": "field/m-01-corrected", "content": "测次原始记录 01（复核更正版）", "quality_decision": "included", "decision_reason": "复裁通过"}, measurementBindings},
		{"m-02", "req-bind-m02", map[string]any{"evidence_type": "rating_measurement", "observed_at": "2026-07-01T02:00:00Z", "water_level_m": 2.0, "discharge_m3s": 20.0, "source_ref": "field/m-02", "content": "测次原始记录 02", "quality_decision": "included", "decision_reason": "复裁通过"}, measurementBindings},
		{"m-03", "req-bind-m03", map[string]any{"evidence_type": "rating_measurement", "observed_at": "2026-07-01T03:00:00Z", "water_level_m": 3.0, "discharge_m3s": 30.0, "source_ref": "field/m-03", "content": "测次原始记录 03", "quality_decision": "included", "decision_reason": "复裁通过"}, measurementBindings},
		{"m-04", "req-bind-m04", map[string]any{"evidence_type": "rating_measurement", "observed_at": "2026-07-01T04:00:00Z", "water_level_m": 2.5, "discharge_m3s": 25.0, "source_ref": "field/m-04", "content": "测次原始记录 04", "quality_decision": "included", "decision_reason": "复裁通过"}, measurementBindings},
		{"x-01", "req-bind-x01", map[string]any{"evidence_type": "cross_section", "observed_at": "2026-06-30T10:00:00Z", "source_ref": "survey/x-01", "content": "断面测量坐标集", "quality_decision": "included", "decision_reason": "闭合差合格"}, []map[string]any{{"instrument_kind": "survey_equipment", "instrument_id": "survey-303", "qualification_id": "q-survey", "certificate_digest": qualificationDigests["q-survey"]}}},
	}
	for _, x := range boundEvidence {
		if err = bind(x.id, x.request, x.payload, x.bindings); err != nil {
			return err
		}
	}
	var quality struct {
		Verdict string `json:"verdict"`
	}
	if _, err = r.request(ctx, http.MethodGet, basePath+"/evidence/quality-preflight", nil, 200, &quality); err != nil || quality.Verdict != "pass" {
		return fmt.Errorf("质量门禁预检失败: %v", err)
	}
	var coverage struct {
		Verdict string `json:"verdict"`
	}
	if _, err = r.request(ctx, http.MethodGet, basePath+"/instrument-qualifications/coverage-matrix", nil, 200, &coverage); err != nil || coverage.Verdict != "pass" {
		return fmt.Errorf("仪器覆盖矩阵检查失败: %v", err)
	}
	var baselinePreflight struct {
		ProposedBaselineDigest string `json:"proposed_baseline_digest"`
	}
	if _, err = r.request(ctx, http.MethodGet, basePath+"/baseline-preflight", nil, 200, &baselinePreflight); err != nil {
		return err
	}
	if err = add(basePath+"/freeze-baseline", "req-freeze", "owner-a", map[string]any{"proposed_baseline_digest": baselinePreflight.ProposedBaselineDigest}); err != nil {
		return err
	}
	if err = add(basePath+"/qualify-evidence", "req-qualify", "quality-a", map[string]any{}); err != nil {
		return err
	}
	if err = add(basePath+"/assessments", "req-assess", "modeler-b", map[string]any{"run_id": "run-001", "historical_high_m": 3.1, "max_extension_ratio": .25}); err != nil {
		return err
	}
	var diagnostics struct {
		Integrity string `json:"integrity"`
		Details   []any  `json:"details"`
	}
	if _, err = r.request(ctx, http.MethodGet, basePath+"/assessments/diagnostics?band=high", nil, 200, &diagnostics); err != nil || diagnostics.Integrity != "pass" || len(diagnostics.Details) == 0 {
		return fmt.Errorf("残差诊断查询失败: %v", err)
	}
	if err = add(basePath+"/deviations", "req-deviation", "quality-a", map[string]any{"deviation_id": "dev-001", "source_gate": "field_record_completeness", "severity": "minor"}); err != nil {
		return err
	}
	if err = add(basePath+"/deviations/dev-001/containment", "req-contain", "handler-b", map[string]any{"description": "暂停引用受影响记录"}); err != nil {
		return err
	}
	if err = add(basePath+"/deviations/dev-001/root-cause", "req-analyze", "handler-b", map[string]any{"description": "现场记录复核签名遗漏"}); err != nil {
		return err
	}
	if err = add(basePath+"/deviations/dev-001/correction", "req-correct", "handler-b", map[string]any{"description": "补签并执行双人复核", "evidence_ref": "verification/dev-001"}); err != nil {
		return err
	}
	if err = add(basePath+"/deviations/dev-001/verification", "req-verify", "verifier-c", map[string]any{"retest_id": "retest-dev-001", "verified_by": "verifier-c", "actual": 0.0, "threshold": 0.1}); err != nil {
		return err
	}
	if c.State != "deviations_closed" {
		return fmt.Errorf("偏差闭环未推进状态")
	}
	issues := []map[string]any{{"issue_id": "review-issue-1", "category": "evidence", "related_id": "m-01", "description": "需说明更正版本来源", "required_action": "补充版本核验引用"}, {"issue_id": "review-issue-2", "category": "diagnostic", "related_id": "run-001", "description": "需确认高水位残差", "required_action": "补充诊断复核引用"}}
	var reviewPreflight struct {
		MaterialsDigest string `json:"materials_digest"`
	}
	if _, err = r.request(ctx, http.MethodGet, basePath+"/reviews/preflight?reviewer_id=reviewer-d", nil, 200, &reviewPreflight); err != nil {
		return err
	}
	if err = add(basePath+"/reviews", "req-review-return", "reviewer-d", map[string]any{"reviewer_id": "reviewer-d", "decision": "return", "comment": "两项问题待处理", "issues": issues, "materials_digest": reviewPreflight.MaterialsDigest}); err != nil {
		return err
	}
	if err = add(basePath+"/reviews/issues/review-issue-1/responses", "req-review-response-1", "owner-a", map[string]any{"response": "已核对更正链", "evidence_ref": "review/evidence-chain"}); err != nil {
		return err
	}
	incomplete := merge(meta("req-review-resubmit-incomplete", "owner-a", c.Revision), map[string]any{"reviewer_id": "reviewer-j"})
	if _, err = r.request(ctx, http.MethodPost, basePath+"/reviews/resubmit", incomplete, 422, nil); err != nil {
		return fmt.Errorf("未完成问题项的重提门禁失败: %w", err)
	}
	if err = add(basePath+"/reviews/issues/review-issue-2/responses", "req-review-response-2", "owner-a", map[string]any{"response": "已复算高水位残差", "evidence_ref": "review/high-band"}); err != nil {
		return err
	}
	if err = add(basePath+"/reviews/resubmit", "req-review-resubmit", "owner-a", map[string]any{"reviewer_id": "reviewer-j"}); err != nil {
		return err
	}
	if _, err = r.request(ctx, http.MethodGet, basePath+"/reviews/preflight?reviewer_id=reviewer-j", nil, 200, &reviewPreflight); err != nil {
		return err
	}
	if err = add(basePath+"/reviews", "req-review-pass", "reviewer-j", map[string]any{"reviewer_id": "reviewer-j", "decision": "pass", "comment": "整改重提内容通过", "issues": []any{}, "materials_digest": reviewPreflight.MaterialsDigest}); err != nil {
		return err
	}
	trialUntil := time.Now().UTC().Add(2 * time.Second)
	if err = add(basePath+"/trial-decisions", "req-trial", "authority-e", map[string]any{"decision_id": "decision-trial-001", "authorized_by": "authority-e", "effective_from": "2026-07-02T00:00:00Z", "effective_until": trialUntil, "rollback_condition": "任一独立校核测次相对偏差超过 10%"}); err != nil {
		return err
	}
	if err = add(basePath+"/trial-observations", "req-observation-trigger", "observer-f", map[string]any{"observation_id": "obs-trigger", "observed_at": "2026-07-03T00:00:00Z", "water_level_m": 1.5, "measured_discharge_m3s": 15.0, "predicted_discharge_m3s": 18.0}); err != nil {
		return err
	}
	if c.State != "trial_suspended" {
		return fmt.Errorf("超限测次未暂停试用")
	}
	if err = add(basePath+"/trial-suspensions/investigation", "req-investigation", "handler-h", map[string]any{"cause_category": "现场测流扰动", "impact_started_at": "2026-07-03T00:00:00Z", "impact_ended_at": "2026-07-03T23:59:59Z", "action": "复核仪器布置并补充独立测次", "evidence_ref": "trial/investigation-001"}); err != nil {
		return err
	}
	if err = add(basePath+"/trial-suspensions/recovery", "req-recovery", "reviewer-i", map[string]any{"reviewer_id": "reviewer-i", "observation_id": "obs-confirm", "observed_at": "2026-07-04T00:00:00Z", "water_level_m": 2.0, "measured_discharge_m3s": 20.0, "predicted_discharge_m3s": 20.2, "boundary_unchanged": true}); err != nil {
		return err
	}
	if c.State != "trial_active" {
		return fmt.Errorf("独立恢复判定未恢复试用")
	}
	observations := []map[string]any{{"observation_id": "obs-02", "observed_at": "2026-07-05T00:00:00Z", "water_level_m": 1.05, "measured_discharge_m3s": 10.5, "predicted_discharge_m3s": 10.4}, {"observation_id": "obs-03", "observed_at": "2026-07-06T00:00:00Z", "water_level_m": 3.0, "measured_discharge_m3s": 30.0, "predicted_discharge_m3s": 30.1}}
	for i, item := range observations {
		if err = add(basePath+"/trial-observations", fmt.Sprintf("req-observation-%d", i), "observer-f", item); err != nil {
			return err
		}
	}
	var progress struct {
		Qualified bool `json:"qualified"`
	}
	if _, err = r.request(ctx, http.MethodGet, basePath+"/trial-progress", nil, 200, &progress); err != nil || !progress.Qualified {
		return fmt.Errorf("试用进度查询失败: %v", err)
	}
	if wait := time.Until(trialUntil.Add(20 * time.Millisecond)); wait > 0 {
		timer := time.NewTimer(wait)
		select {
		case <-ctx.Done():
			timer.Stop()
			return ctx.Err()
		case <-timer.C:
		}
	}
	if err = add(basePath+"/trial-expiry-settlements", "req-trial-expiry", "authority-e", map[string]any{}); err != nil {
		return err
	}
	if c.State != "trial_qualified" {
		return fmt.Errorf("试用到期结算未形成转正资格")
	}
	var preflight struct {
		CurrentVersionDigest string `json:"current_version_digest"`
		Eligible             bool   `json:"eligible"`
	}
	if _, err = r.request(ctx, http.MethodGet, basePath+"/activation-preflight?effective_from=2026-08-01T00:00:00Z", nil, 200, &preflight); err != nil {
		return err
	}
	if !preflight.Eligible || preflight.CurrentVersionDigest == "" {
		return fmt.Errorf("启用预检未通过")
	}
	if err = add(basePath+"/activation-decisions", "req-activate", "authority-g", map[string]any{"decision_id": "decision-active-001", "authorized_by": "authority-g", "effective_from": "2026-08-01T00:00:00Z", "rollback_condition": "后续洪水复核出现系统偏差时回退", "current_version_digest": preflight.CurrentVersionDigest}); err != nil {
		return err
	}
	activationRevision := c.Revision
	stale := merge(meta("req-stale", "owner-a", activationRevision-1), map[string]any{})
	if _, err = r.request(ctx, http.MethodPost, basePath+"/archive", stale, 409, nil); err != nil {
		return fmt.Errorf("revision 冲突验证失败: %w", err)
	}
	if err = add(basePath+"/archive", "req-archive", "records-h", map[string]any{}); err != nil {
		return err
	}
	var archive struct {
		Archive struct {
			Case   caseResponse `json:"case"`
			Digest string       `json:"archive_digest"`
		} `json:"archive"`
		IntegrityOK bool `json:"integrity_ok"`
	}
	if _, err = r.request(ctx, http.MethodGet, basePath+"/archive", nil, 200, &archive); err != nil {
		return err
	}
	if !archive.IntegrityOK || archive.Archive.Case.State != "archived" || archive.Archive.Digest == "" {
		return fmt.Errorf("认证档案完整性验证失败")
	}
	var verification struct {
		IntegrityOK      bool `json:"integrity_ok"`
		ManifestPosition *int `json:"manifest_position"`
	}
	verificationPath := basePath + "/archive/verification?archive_digest=" + archive.Archive.Digest + "&kind=assessment_run&id=run-001"
	if _, err = r.request(ctx, http.MethodGet, verificationPath, nil, 200, &verification); err != nil || !verification.IntegrityOK || verification.ManifestPosition == nil {
		return fmt.Errorf("档案逐项证明失败: %v", err)
	}
	var assessmentReplay struct {
		Matched     bool   `json:"matched"`
		InputDigest string `json:"input_digest"`
	}
	if _, err = r.request(ctx, http.MethodGet, basePath+"/assessments/run-001/replay-verification", nil, 200, &assessmentReplay); err != nil || !assessmentReplay.Matched || assessmentReplay.InputDigest == "" {
		return fmt.Errorf("归档评估重放核验失败: %v", err)
	}
	var current map[string]string
	if _, err = r.request(ctx, http.MethodGet, "/api/v1/stations/station-self-check/current-curve", nil, 200, &current); err != nil {
		return err
	}
	if current["curve_version"] != "curve-2026-flood-v1" {
		return fmt.Errorf("测站当前曲线指针不正确")
	}
	var history struct {
		Versions []any `json:"versions"`
	}
	if _, err = r.request(ctx, http.MethodGet, "/api/v1/stations/station-self-check/curve-history", nil, 200, &history); err != nil || len(history.Versions) != 1 {
		return fmt.Errorf("测站版本履历检查失败: %v", err)
	}
	var continuity struct {
		OverallVerdict string `json:"overall_verdict"`
	}
	if _, err = r.request(ctx, http.MethodGet, "/api/v1/stations/station-self-check/certification-continuity", nil, 200, &continuity); err != nil || continuity.OverallVerdict != "pass" {
		return fmt.Errorf("测站认证连续性核查失败: %v", err)
	}
	var timeline struct {
		Events []any `json:"events"`
	}
	if _, err = r.request(ctx, http.MethodGet, basePath+"/timeline", nil, 200, &timeline); err != nil {
		return err
	}
	if len(timeline.Events) < 20 {
		return fmt.Errorf("审计时间线事件不足")
	}
	return nil
}
