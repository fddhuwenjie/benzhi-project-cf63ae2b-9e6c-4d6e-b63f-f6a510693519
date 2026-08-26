package assessment

import (
	"sort"
	"time"

	"benzhi-project-cf63ae2b-9e6c-4d6e-b63f-f6a510693519/internal/domain"
)

// InvalidatedBindings只检查冻结清单中的实际绑定，不会用同类设备替换。
func (e *Engine) InvalidatedBindings(c *domain.Case, qualificationID, invalidationType string) ([]domain.InstrumentImpact, error) {
	evidence, err := c.FrozenEvidence()
	if err != nil {
		return nil, err
	}
	qualifications, err := c.FrozenQualifications()
	if err != nil {
		return nil, err
	}
	matrix := e.CoverageMatrix(evidence, qualifications)
	cells := map[string]CoverageCell{}
	for _, cell := range matrix.Cells {
		cells[cell.EvidenceID+"/"+cell.InstrumentKind] = cell
	}
	out := []domain.InstrumentImpact{}
	for _, item := range evidence {
		for _, binding := range item.InstrumentBindings {
			if binding.QualificationID != qualificationID {
				continue
			}
			reason := "certificate_" + invalidationType
			if cell := cells[item.EvidenceID+"/"+binding.InstrumentKind]; !cell.Covered && cell.IssueCode != "" {
				reason = cell.IssueCode + ";certificate_" + invalidationType
			}
			out = append(out, domain.InstrumentImpact{EvidenceID: item.EvidenceID, InstrumentKind: binding.InstrumentKind, ObservedAt: item.ObservedAt, QualificationID: qualificationID, CoverageFailure: reason})
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].EvidenceID == out[j].EvidenceID {
			return out[i].InstrumentKind < out[j].InstrumentKind
		}
		return out[i].EvidenceID < out[j].EvidenceID
	})
	return out, nil
}

// TrialProgressAtExpiry固定使用该试用决定的有效期和已清除污染区间后的测次。
func (e *Engine) TrialProgressAtExpiry(c *domain.Case, decision domain.Decision) domain.TrialProgress {
	copyCase := *c
	copyCase.TrialObservations = nil
	for _, observation := range c.TrialObservations {
		if observation.ObservedAt.Before(decision.EffectiveFrom) || decision.EffectiveUntil == nil || observation.ObservedAt.After(*decision.EffectiveUntil) {
			continue
		}
		if observation.TrialDecisionID != "" && observation.TrialDecisionID != decision.DecisionID {
			continue
		}
		copyCase.TrialObservations = append(copyCase.TrialObservations, observation)
	}
	progress := e.TrialProgress(&copyCase)
	sort.Strings(progress.UnmetGates)
	return progress
}

func within(value, start, end time.Time) bool {
	return !value.Before(start) && !value.After(end)
}
