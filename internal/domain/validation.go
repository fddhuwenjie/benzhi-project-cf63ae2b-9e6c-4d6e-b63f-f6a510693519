package domain

import (
	"fmt"
	"math"
	"sort"
	"strings"
	"time"
)

var evidenceTypes = map[string]bool{
	"rating_measurement":    true,
	"cross_section":         true,
	"field_record":          true,
	"historical_flood_mark": true,
}

var instrumentKinds = map[string]bool{
	"current_meter":     true,
	"water_level_gauge": true,
	"survey_equipment":  true,
}

func ValidateEvidence(e Evidence) error {
	if strings.TrimSpace(e.EvidenceID) == "" || len(e.EvidenceID) > 120 {
		return fmt.Errorf("%w: evidence_id 必填且不超过 120 字符", ErrInvalid)
	}
	if !evidenceTypes[e.EvidenceType] {
		return fmt.Errorf("%w: evidence_type %q 无效", ErrInvalid, e.EvidenceType)
	}
	if e.ObservedAt.IsZero() {
		return fmt.Errorf("%w: observed_at 必填", ErrInvalid)
	}
	if strings.TrimSpace(e.SourceRef) == "" || len(e.SourceRef) > 500 {
		return fmt.Errorf("%w: source_ref 必填且不超过 500 字符", ErrInvalid)
	}
	if len(e.ContentDigest) != 64 {
		return fmt.Errorf("%w: content_digest 必须是 SHA-256 摘要", ErrInvalid)
	}
	if e.QualityDecision != "pending" && e.QualityDecision != "pending_explanation" && e.QualityDecision != "included" && e.QualityDecision != "excluded" {
		return fmt.Errorf("%w: quality_decision 无效", ErrInvalid)
	}
	if e.QualityDecision == "excluded" && strings.TrimSpace(e.DecisionReason) == "" {
		return fmt.Errorf("%w: 排除证据必须记录原因", ErrInvalid)
	}
	if e.EvidenceType == "rating_measurement" {
		if e.WaterLevelM == nil || e.DischargeM3S == nil {
			return fmt.Errorf("%w: 评级测次必须提供水位和流量", ErrInvalid)
		}
		if math.IsNaN(*e.WaterLevelM) || math.IsInf(*e.WaterLevelM, 0) || *e.WaterLevelM < -20 || *e.WaterLevelM > 100 {
			return fmt.Errorf("%w: water_level_m 超出工程范围", ErrInvalid)
		}
		if math.IsNaN(*e.DischargeM3S) || math.IsInf(*e.DischargeM3S, 0) || *e.DischargeM3S <= 0 || *e.DischargeM3S > 1e7 {
			return fmt.Errorf("%w: discharge_m3s 超出工程范围", ErrInvalid)
		}
	}
	if e.EvidenceType == "historical_flood_mark" && e.WaterLevelM == nil {
		return fmt.Errorf("%w: 历史洪痕必须提供水位", ErrInvalid)
	}
	if e.EvidenceType == "historical_flood_mark" {
		if strings.TrimSpace(e.SourceRef) == "" || strings.TrimSpace(e.FloodEventID) == "" || strings.TrimSpace(e.DatumID) == "" || e.VerticalUncertaintyM == nil || math.IsNaN(*e.VerticalUncertaintyM) || math.IsInf(*e.VerticalUncertaintyM, 0) || *e.VerticalUncertaintyM <= 0 {
			return fmt.Errorf("%w: 历史洪痕必须提供 flood_event_id、datum_id、来源引用和正的 vertical_uncertainty_m", ErrInvalid)
		}
		if e.ConfidenceLevel != "high" && e.ConfidenceLevel != "medium" && e.ConfidenceLevel != "low" {
			return fmt.Errorf("%w: 历史洪痕 confidence_level 无效", ErrInvalid)
		}
	}
	seenBindings := map[string]bool{}
	for _, b := range e.InstrumentBindings {
		if !instrumentKinds[b.InstrumentKind] || b.InstrumentID == "" || b.QualificationID == "" || len(b.CertificateDigest) != 64 {
			return fmt.Errorf("%w: instrument_bindings 字段不完整", ErrInvalid)
		}
		if seenBindings[b.InstrumentKind] {
			return fmt.Errorf("%w: instrument_kind %s 重复绑定", ErrInvalid, b.InstrumentKind)
		}
		seenBindings[b.InstrumentKind] = true
	}
	return nil
}

func ValidateQualification(q Qualification) error {
	if strings.TrimSpace(q.QualificationID) == "" || strings.TrimSpace(q.InstrumentID) == "" || strings.TrimSpace(q.CertificateRef) == "" {
		return fmt.Errorf("%w: 资格、仪器和证书标识均为必填", ErrInvalid)
	}
	if !instrumentKinds[q.InstrumentKind] {
		return fmt.Errorf("%w: instrument_kind %q 无效", ErrInvalid, q.InstrumentKind)
	}
	if q.CalibratedAt.IsZero() || q.ValidUntil.IsZero() || q.UsageStartedAt.IsZero() || q.UsageEndedAt.IsZero() {
		return fmt.Errorf("%w: 校准与使用时段必须完整", ErrInvalid)
	}
	if q.ValidUntil.Before(q.CalibratedAt) {
		return fmt.Errorf("%w: 校准有效期结束早于校准日期", ErrInvalid)
	}
	if q.UsageEndedAt.Before(q.UsageStartedAt) {
		return fmt.Errorf("%w: 仪器使用结束早于开始", ErrInvalid)
	}
	if q.Verdict != "qualified" && q.Verdict != "unqualified" {
		return fmt.Errorf("%w: 仪器资格结论无效", ErrInvalid)
	}
	if q.Version < 1 || len(q.Digest) != 64 {
		return fmt.Errorf("%w: 仪器资格版本或摘要无效", ErrInvalid)
	}
	return nil
}

func ValidateAssessment(a Assessment) error {
	if a.RunID == "" || a.InputDigest == "" || a.MethodVersion == "" {
		return fmt.Errorf("%w: 评估运行标识、输入摘要和方法版本必填", ErrInvalid)
	}
	if a.LowerBoundM >= a.UpperBoundM {
		return fmt.Errorf("%w: 评估适用边界无效", ErrInvalid)
	}
	if a.ExtrapolationRatio < 0 || math.IsNaN(a.ExtrapolationRatio) || math.IsInf(a.ExtrapolationRatio, 0) {
		return fmt.Errorf("%w: 外推比例无效", ErrInvalid)
	}
	if len(a.BoundaryDiagnostics) > 0 {
		if err := ValidateBoundaryRequest(a.RequestedLowerBoundM, a.RequestedUpperBoundM, a.MaxLowExtensionRatio, a.MaxHighExtensionRatio); err != nil {
			return err
		}
		if len(a.BoundaryDiagnostics) != 2 || a.BoundaryDiagnostics[0].Side != "low" || a.BoundaryDiagnostics[1].Side != "high" {
			return fmt.Errorf("%w: 双端边界诊断必须按 low、high 保存", ErrInvalid)
		}
		for _, d := range a.BoundaryDiagnostics {
			if d.Verdict != "pass" && d.Verdict != "fail" {
				return fmt.Errorf("%w: 边界诊断结论无效", ErrInvalid)
			}
		}
	}
	if a.Verdict != "pass" && a.Verdict != "fail" {
		return fmt.Errorf("%w: 评估结论无效", ErrInvalid)
	}
	for name, value := range a.Parameters {
		if math.IsNaN(value) || math.IsInf(value, 0) {
			return fmt.Errorf("%w: 参数 %s 不是有限数", ErrInvalid, name)
		}
	}
	for name, value := range a.ResidualMetrics {
		if math.IsNaN(value) || math.IsInf(value, 0) {
			return fmt.Errorf("%w: 指标 %s 不是有限数", ErrInvalid, name)
		}
	}
	previous := ""
	seen := map[string]bool{}
	bandCounts := map[string]int{}
	for _, d := range a.ResidualDetails {
		if d.EvidenceID == "" || seen[d.EvidenceID] {
			return fmt.Errorf("%w: 残差明细 evidence_id 缺失或重复", ErrInvalid)
		}
		seen[d.EvidenceID] = true
		if previous != "" && d.EvidenceID < previous {
			return fmt.Errorf("%w: 残差明细未按 evidence_id 排序", ErrInvalid)
		}
		previous = d.EvidenceID
		if d.Band != "low" && d.Band != "medium" && d.Band != "high" {
			return fmt.Errorf("%w: 残差分带无效", ErrInvalid)
		}
		for _, v := range []float64{d.WaterLevelM, d.MeasuredM3S, d.PredictedM3S, d.AbsoluteResidual, d.RelativeResidual, d.Threshold} {
			if math.IsNaN(v) || math.IsInf(v, 0) {
				return fmt.Errorf("%w: 残差明细包含非有限数", ErrInvalid)
			}
		}
		if d.Verdict != "pass" && d.Verdict != "fail" {
			return fmt.Errorf("%w: 残差明细结论无效", ErrInvalid)
		}
		bandCounts[d.Band]++
	}
	for _, b := range a.BandSummaries {
		if b.SampleCount != bandCounts[b.Band] {
			return fmt.Errorf("%w: 分带 %s 汇总数量不一致", ErrInvalid, b.Band)
		}
		if b.Verdict != "pass" && b.Verdict != "fail" {
			return fmt.Errorf("%w: 分带结论无效", ErrInvalid)
		}
	}
	seenInfluence := map[string]bool{}
	previousScore := math.Inf(1)
	previousID := ""
	for _, d := range a.InfluenceDetails {
		if d.EvidenceID == "" || seenInfluence[d.EvidenceID] {
			return fmt.Errorf("%w: 影响度 evidence_id 缺失或重复", ErrInvalid)
		}
		seenInfluence[d.EvidenceID] = true
		for _, v := range []float64{d.ParameterChangeRatio, d.TargetPredictionChangeRatio, d.UpperBoundChangeM, d.InfluenceScore, d.Threshold} {
			if math.IsNaN(v) || math.IsInf(v, 0) {
				return fmt.Errorf("%w: 影响度包含非有限数", ErrInvalid)
			}
		}
		if d.InfluenceScore > previousScore || (d.InfluenceScore == previousScore && previousID != "" && d.EvidenceID < previousID) {
			return fmt.Errorf("%w: 影响度排序不稳定", ErrInvalid)
		}
		if d.Verdict != "pass" && d.Verdict != "fail" {
			return fmt.Errorf("%w: 影响度结论无效", ErrInvalid)
		}
		previousScore, previousID = d.InfluenceScore, d.EvidenceID
	}
	if len(a.InfluenceDetails) != len(a.ResidualDetails) {
		return fmt.Errorf("%w: 影响度未覆盖全部纳入测次", ErrInvalid)
	}
	return nil
}

func ValidateDeviation(d Deviation) error {
	if d.DeviationID == "" || d.SourceGate == "" {
		return fmt.Errorf("%w: 偏差标识和来源门禁必填", ErrInvalid)
	}
	if d.Severity != "minor" && d.Severity != "major" && d.Severity != "critical" {
		return fmt.Errorf("%w: 偏差严重度无效", ErrInvalid)
	}
	if d.State != "open" && d.State != "contained" && d.State != "analyzed" && d.State != "corrected" && d.State != "correction_required" && d.State != "verified" {
		return fmt.Errorf("%w: 偏差状态无效", ErrInvalid)
	}
	if d.State == "verified" && (d.Containment == "" || d.RootCause == "" || d.CorrectiveAction == "" || d.VerificationEvidenceRef == "" || d.VerifiedBy == "" || d.VerifiedAt == nil || d.RetestVerdict != "pass") {
		return fmt.Errorf("%w: 已验证偏差的闭环字段不完整", ErrInvalid)
	}
	if d.CreatedAt.IsZero() || d.OriginalDueAt.IsZero() || d.DueAt.IsZero() || d.OriginalDueAt.Before(d.CreatedAt) || d.DueAt.Before(d.OriginalDueAt) {
		return fmt.Errorf("%w: 偏差期限无效", ErrInvalid)
	}
	return nil
}

func ValidateDecision(d Decision) error {
	if d.DecisionID == "" || d.CurveVersion == "" || d.AuthorizedBy == "" || d.EffectiveFrom.IsZero() {
		return fmt.Errorf("%w: 决定标识、曲线版本、授权人和生效时刻必填", ErrInvalid)
	}
	if d.DecisionType != "trial" && d.DecisionType != "activation" {
		return fmt.Errorf("%w: decision_type 无效", ErrInvalid)
	}
	if d.LowerBoundM >= d.UpperBoundM || d.RollbackCondition == "" {
		return fmt.Errorf("%w: 决定边界或回退条件无效", ErrInvalid)
	}
	if d.DecisionType == "trial" && (d.EffectiveUntil == nil || !d.EffectiveUntil.After(d.EffectiveFrom)) {
		return fmt.Errorf("%w: 试用决定必须具有有效结束时刻", ErrInvalid)
	}
	if d.DecisionType == "activation" && d.EffectiveUntil != nil {
		return fmt.Errorf("%w: 正式启用决定不得设置结束时刻", ErrInvalid)
	}
	if d.Status != "" && d.Status != "active" && d.Status != "invalidated" && d.Status != "expired_unqualified" && d.Status != "qualified" {
		return fmt.Errorf("%w: 决定状态无效", ErrInvalid)
	}
	if d.Status == "invalidated" && (d.InvalidatedAt == nil || d.InvalidationReason == "") {
		return fmt.Errorf("%w: 已失效决定缺少失效摘要", ErrInvalid)
	}
	return nil
}

func ValidateTrialObservation(o TrialObservation) error {
	if o.ObservationID == "" || o.ObservedAt.IsZero() {
		return fmt.Errorf("%w: 校核测次标识和时刻必填", ErrInvalid)
	}
	if o.MeasuredDischargeM3S <= 0 || o.PredictedDischargeM3S <= 0 {
		return fmt.Errorf("%w: 校核流量必须大于零", ErrInvalid)
	}
	if math.IsNaN(o.RelativeBias) || math.IsInf(o.RelativeBias, 0) {
		return fmt.Errorf("%w: 相对偏差不是有限数", ErrInvalid)
	}
	if o.Verdict != "continue" && o.Verdict != "suspend" {
		return fmt.Errorf("%w: 校核测次结论无效", ErrInvalid)
	}
	if o.RecordState != "" && o.RecordState != "active" && o.RecordState != "superseded" {
		return fmt.Errorf("%w: trial observation record_state 无效", ErrInvalid)
	}
	if o.RecordState == "superseded" && (o.SupersededBy == "" || o.SupersededReason == "" || o.ReplacementEvidenceRef == "") {
		return fmt.Errorf("%w: 作废测次替代关系不完整", ErrInvalid)
	}
	return nil
}

func (c *Case) ValidateConsistency() error {
	if err := ValidateCreate(c); err != nil {
		return err
	}
	if c.Revision < 1 {
		return fmt.Errorf("%w: revision 必须为正数", ErrInvalid)
	}
	evidenceIDs := map[string]bool{}
	for _, e := range c.Evidence {
		if err := ValidateEvidence(e); err != nil {
			return fmt.Errorf("证据 %s: %w", e.EvidenceID, err)
		}
		if evidenceIDs[e.EvidenceID] {
			return fmt.Errorf("%w: evidence_id %s 重复", ErrInvalid, e.EvidenceID)
		}
		evidenceIDs[e.EvidenceID] = true
		if err := ValidateStoredBindingReferences(e, c.Qualifications); err != nil {
			return err
		}
	}
	chains := map[string][]EvidenceVersion{}
	versionKeys := map[string]bool{}
	for _, v := range c.EvidenceVersions {
		if !evidenceIDs[v.EvidenceID] {
			return fmt.Errorf("%w: 证据版本引用不存在的 evidence_id %s", ErrInvalid, v.EvidenceID)
		}
		key := fmt.Sprintf("%s/%d", v.EvidenceID, v.Version)
		if versionKeys[key] || v.Version < 1 {
			return fmt.Errorf("%w: 证据版本号重复或无效", ErrInvalid)
		}
		versionKeys[key] = true
		chains[v.EvidenceID] = append(chains[v.EvidenceID], v)
	}
	for id, items := range chains {
		sort.Slice(items, func(i, j int) bool { return items[i].Version < items[j].Version })
		for i, v := range items {
			if v.Version != i+1 {
				return fmt.Errorf("%w: 证据 %s 版本号不连续", ErrInvalid, id)
			}
			if i > 0 && v.PreviousDigest != items[i-1].ContentDigest {
				return fmt.Errorf("%w: 证据 %s 替代摘要链断裂", ErrInvalid, id)
			}
		}
		current, _ := c.CurrentEvidence(id)
		if items[len(items)-1].ContentDigest != current.ContentDigest {
			return fmt.Errorf("%w: 证据 %s 最新版本投影不一致", ErrInvalid, id)
		}
	}
	qualificationIDs, instruments := map[string]bool{}, map[string]string{}
	for _, q := range c.Qualifications {
		if err := ValidateQualification(q); err != nil {
			return fmt.Errorf("仪器资格 %s: %w", q.QualificationID, err)
		}
		if qualificationIDs[q.QualificationID] {
			return fmt.Errorf("%w: qualification_id %s 重复", ErrInvalid, q.QualificationID)
		}
		qualificationIDs[q.QualificationID] = true
		key := q.InstrumentID + "/" + q.CertificateRef
		if previous := instruments[key]; previous != "" && previous != q.QualificationID {
			return fmt.Errorf("%w: 仪器证书追溯关系重复", ErrInvalid)
		}
		instruments[key] = q.QualificationID
		for _, prior := range c.Qualifications {
			if prior.QualificationID == q.QualificationID || prior.InstrumentID != q.InstrumentID || prior.CertificateRef == q.CertificateRef {
				continue
			}
			if q.CalibratedAt.Before(prior.ValidUntil) && prior.CalibratedAt.Before(q.ValidUntil) {
				return fmt.Errorf("%w: instrument_id %s 存在相互矛盾的证书有效区间", ErrInvalid, q.InstrumentID)
			}
		}
	}
	qualificationChains := map[string][]QualificationVersion{}
	for _, v := range c.QualificationVersions {
		qualificationChains[v.QualificationID] = append(qualificationChains[v.QualificationID], v)
	}
	for id, items := range qualificationChains {
		sort.Slice(items, func(i, j int) bool { return items[i].Version < items[j].Version })
		for i, v := range items {
			if v.Version != i+1 || (i > 0 && v.PreviousDigest != items[i-1].Digest) {
				return fmt.Errorf("%w: 仪器资格 %s 版本链断裂", ErrInvalid, id)
			}
		}
		current, err := c.Qualification(id)
		if err != nil || current.Digest != items[len(items)-1].Digest {
			return fmt.Errorf("%w: 仪器资格 %s 当前投影不一致", ErrInvalid, id)
		}
	}
	if c.Assessment != nil {
		if err := ValidateAssessment(*c.Assessment); err != nil {
			return err
		}
	}
	deviationIDs := map[string]bool{}
	for _, d := range c.Deviations {
		if err := ValidateDeviation(d); err != nil {
			return err
		}
		if deviationIDs[d.DeviationID] {
			return fmt.Errorf("%w: deviation_id %s 重复", ErrInvalid, d.DeviationID)
		}
		deviationIDs[d.DeviationID] = true
	}
	decisionIDs := map[string]bool{}
	for _, d := range c.Decisions {
		if err := ValidateDecision(d); err != nil {
			return err
		}
		if decisionIDs[d.DecisionID] {
			return fmt.Errorf("%w: decision_id %s 重复", ErrInvalid, d.DecisionID)
		}
		decisionIDs[d.DecisionID] = true
	}
	observationIDs := map[string]bool{}
	ordered := append([]TrialObservation(nil), c.TrialObservations...)
	sort.Slice(ordered, func(i, j int) bool { return ordered[i].ObservedAt.Before(ordered[j].ObservedAt) })
	for _, o := range ordered {
		if err := ValidateTrialObservation(o); err != nil {
			return err
		}
		if observationIDs[o.ObservationID] {
			return fmt.Errorf("%w: observation_id %s 重复", ErrInvalid, o.ObservationID)
		}
		observationIDs[o.ObservationID] = true
	}
	if c.BaselineManifest != nil {
		if c.BaselineDigest != c.BaselineManifest.Digest || len(c.BaselineManifest.Items) == 0 {
			return fmt.Errorf("%w: 基线清单摘要不一致", ErrInvalid)
		}
	}
	invalidationIDs := map[string]bool{}
	for _, x := range c.InstrumentInvalidations {
		if x.InvalidationID == "" || x.QualificationID == "" || x.OriginalCertificateDigest == "" || x.ReportedAt.IsZero() || invalidationIDs[x.InvalidationID] {
			return fmt.Errorf("%w: 仪器失效通报不完整或重复", ErrInvalid)
		}
		invalidationIDs[x.InvalidationID] = true
	}
	settlementDecisions := map[string]bool{}
	for _, x := range c.TrialExpirySettlements {
		if x.SettlementID == "" || x.DecisionID == "" || x.SettledAt.IsZero() || settlementDecisions[x.DecisionID] || (x.Verdict != "qualified" && x.Verdict != "expired_unqualified") {
			return fmt.Errorf("%w: 试用到期结算不完整或重复", ErrInvalid)
		}
		settlementDecisions[x.DecisionID] = true
	}
	if c.State == Archived && c.ArchivedAt == nil {
		return fmt.Errorf("%w: 归档案件缺少 archived_at", ErrInvalid)
	}
	if c.State != Archived && c.ArchivedAt != nil {
		return fmt.Errorf("%w: 未归档案件不得设置 archived_at", ErrInvalid)
	}
	return nil
}

func NormalizedTime(t time.Time) time.Time { return t.UTC().Round(0) }
