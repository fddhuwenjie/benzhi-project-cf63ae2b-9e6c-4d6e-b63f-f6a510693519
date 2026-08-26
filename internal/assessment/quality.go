package assessment

import (
	"fmt"
	"math"
	"sort"
	"time"

	"benzhi-project-cf63ae2b-9e6c-4d6e-b63f-f6a510693519/internal/domain"
)

type QualityIssue struct {
	EvidenceID        string `json:"evidence_id"`
	Rule              string `json:"rule"`
	IssueCode         string `json:"issue_code"`
	Severity          string `json:"severity"`
	Message           string `json:"message"`
	SuggestedDecision string `json:"suggested_decision"`
}
type QualityReport struct {
	Evaluated     int            `json:"evaluated"`
	Included      int            `json:"included"`
	Excluded      int            `json:"excluded"`
	Pending       int            `json:"pending"`
	Issues        []QualityIssue `json:"issues"`
	BlockingRules []string       `json:"blocking_rules"`
	Verdict       string         `json:"verdict"`
}

func (e *Engine) EvaluateSamples(items []domain.Evidence) QualityReport {
	report := QualityReport{Issues: []QualityIssue{}, BlockingRules: []string{}, Verdict: "pass"}
	measurements := make([]domain.Evidence, 0)
	seenDigest := map[string]string{}
	seenValues := map[string]string{}
	for _, item := range items {
		report.Evaluated++
		switch item.QualityDecision {
		case "included":
			report.Included++
		case "excluded":
			report.Excluded++
		default:
			report.Pending++
		}
		if item.EvidenceType != "rating_measurement" {
			continue
		}
		measurements = append(measurements, item)
		if item.QualityDecision != "excluded" && (item.WaterLevelM == nil || item.DischargeM3S == nil) {
			report.add(item.EvidenceID, "required_values", "critical", "评级测次缺少水位或流量")
			continue
		}
		if item.WaterLevelM == nil || item.DischargeM3S == nil {
			continue
		}
		if item.QualityDecision != "excluded" && (*item.WaterLevelM < -20 || *item.WaterLevelM > 100) {
			report.add(item.EvidenceID, "water_level_range", "major", "水位超出预设工程范围")
		}
		if item.QualityDecision != "excluded" && (*item.DischargeM3S <= 0 || *item.DischargeM3S > 1e7) {
			report.add(item.EvidenceID, "discharge_range", "major", "流量超出预设工程范围")
		}
		if item.QualityDecision != "excluded" {
			key := fmt.Sprintf("%.9f/%.9f", *item.WaterLevelM, *item.DischargeM3S)
			if previous := seenValues[key]; previous != "" {
				report.add(item.EvidenceID, "duplicate_level_discharge", "major", fmt.Sprintf("水位流量组合与测次 %s 重复", previous))
			} else {
				seenValues[key] = item.EvidenceID
			}
			if previous := seenDigest[item.ContentDigest]; previous != "" {
				report.add(item.EvidenceID, "duplicate_content", "major", fmt.Sprintf("内容与测次 %s 重复", previous))
			} else {
				seenDigest[item.ContentDigest] = item.EvidenceID
			}
		}
		if item.QualityDecision == "excluded" && item.DecisionReason == "" {
			report.add(item.EvidenceID, "exclusion_explanation", "major", "排除测次缺少解释")
		}
		if item.QualityDecision == "pending" || item.QualityDecision == "pending_explanation" {
			report.add(item.EvidenceID, "pending_decision", "major", "测次尚未作出质量裁定")
		}
	}
	sort.Slice(measurements, func(i, j int) bool { return measurements[i].ObservedAt.Before(measurements[j].ObservedAt) })
	for i := 1; i < len(measurements); i++ {
		previous, current := measurements[i-1], measurements[i]
		if current.QualityDecision != "excluded" && previous.QualityDecision != "excluded" && current.ObservedAt.Equal(previous.ObservedAt) && current.WaterLevelM != nil && previous.WaterLevelM != nil && math.Abs(*current.WaterLevelM-*previous.WaterLevelM) < 1e-9 {
			report.add(current.EvidenceID, "duplicate_time_level", "major", fmt.Sprintf("与测次 %s 具有相同观测时刻和水位", previous.EvidenceID))
		}
		if current.QualityDecision != "excluded" && previous.QualityDecision != "excluded" && current.ObservedAt.Sub(previous.ObservedAt) > 180*24*time.Hour {
			report.add(current.EvidenceID, "chronology_gap", "warning", "相邻评级测次间隔超过 180 天，需确认稳定性")
		}
	}
	usable := 0
	for _, item := range measurements {
		if item.QualityDecision == "included" {
			usable++
		}
	}
	if usable < 3 {
		report.add("", "minimum_samples", "critical", "纳入的评级测次少于 3 个")
	}
	blocking := map[string]bool{}
	for _, issue := range report.Issues {
		if issue.Severity == "major" || issue.Severity == "critical" {
			report.Verdict = "fail"
			blocking[issue.Rule] = true
		}
	}
	for code := range blocking {
		report.BlockingRules = append(report.BlockingRules, code)
	}
	sort.Strings(report.BlockingRules)
	return report
}

func (r *QualityReport) add(id, rule, severity, message string) {
	suggested := "included"
	if severity == "major" || severity == "critical" {
		suggested = "pending_explanation"
	}
	r.Issues = append(r.Issues, QualityIssue{EvidenceID: id, Rule: rule, IssueCode: rule, Severity: severity, Message: message, SuggestedDecision: suggested})
}
