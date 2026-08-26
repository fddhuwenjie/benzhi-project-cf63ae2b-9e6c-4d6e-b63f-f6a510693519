package assessment

import (
	"fmt"
	"math"
	"sort"

	"benzhi-project-cf63ae2b-9e6c-4d6e-b63f-f6a510693519/internal/domain"
)

const influenceThreshold = .75

type fitPoint struct {
	id   string
	h, q float64
}

func includedPoints(items []domain.Evidence) []fitPoint {
	out := []fitPoint{}
	for _, x := range items {
		if x.EvidenceType == "rating_measurement" && x.QualityDecision == "included" && x.WaterLevelM != nil && x.DischargeM3S != nil {
			out = append(out, fitPoint{x.EvidenceID, *x.WaterLevelM, *x.DischargeM3S})
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].h == out[j].h {
			return out[i].id < out[j].id
		}
		return out[i].h < out[j].h
	})
	return out
}
func fit(items []fitPoint) (a, b, h0, minH, maxH float64, err error) {
	if len(items) < 3 {
		return 0, 0, 0, 0, 0, fmt.Errorf("%w: influence_insufficient_samples", domain.ErrGate)
	}
	minH, maxH = items[0].h, items[len(items)-1].h
	if maxH-minH <= 0 {
		return 0, 0, 0, 0, 0, fmt.Errorf("%w: influence_singular_matrix", domain.ErrGate)
	}
	h0 = minH - math.Max(.05, (maxH-minH)*.1)
	var sx, sy, sxx, sxy float64
	for _, p := range items {
		x, y := math.Log(p.h-h0), math.Log(p.q)
		sx += x
		sy += y
		sxx += x * x
		sxy += x * y
	}
	n := float64(len(items))
	den := n*sxx - sx*sx
	if math.Abs(den) < 1e-12 {
		return 0, 0, 0, 0, 0, fmt.Errorf("%w: influence_singular_matrix", domain.ErrGate)
	}
	b = (n*sxy - sx*sy) / den
	a = math.Exp((sy - b*sx) / n)
	for _, v := range []float64{a, b, h0} {
		if math.IsNaN(v) || math.IsInf(v, 0) {
			return 0, 0, 0, 0, 0, fmt.Errorf("%w: influence_non_finite", domain.ErrGate)
		}
	}
	return
}
func fitAtOffset(items []fitPoint, h0 float64) (a, b, minH, maxH float64, err error) {
	if len(items) < 3 {
		return 0, 0, 0, 0, fmt.Errorf("%w: influence_insufficient_samples", domain.ErrGate)
	}
	minH, maxH = items[0].h, items[len(items)-1].h
	var sx, sy, sxx, sxy float64
	for _, p := range items {
		if p.h <= h0 {
			return 0, 0, 0, 0, fmt.Errorf("%w: influence_singular_matrix", domain.ErrGate)
		}
		x, y := math.Log(p.h-h0), math.Log(p.q)
		sx += x
		sy += y
		sxx += x * x
		sxy += x * y
	}
	n := float64(len(items))
	den := n*sxx - sx*sx
	if math.Abs(den) < 1e-12 {
		return 0, 0, 0, 0, fmt.Errorf("%w: influence_singular_matrix", domain.ErrGate)
	}
	b = (n*sxy - sx*sy) / den
	a = math.Exp((sy - b*sx) / n)
	return
}
func relativeChange(current, alternative float64) float64 {
	return math.Abs(alternative-current) / math.Max(math.Abs(current), 1e-9)
}
func leaveOneOut(items []domain.Evidence, a, b, h0, targetLevel, historicalHigh float64) ([]domain.InfluenceDetail, error) {
	points := includedPoints(items)
	if len(points) < 4 {
		return nil, fmt.Errorf("%w: influence_insufficient_samples", domain.ErrGate)
	}
	basePrediction := a * math.Pow(targetLevel-h0, b)
	out := make([]domain.InfluenceDetail, 0, len(points))
	baseMin, baseMax := points[0].h, points[len(points)-1].h
	extensionRatio := (targetLevel - baseMax) / math.Max(baseMax-baseMin, 1e-9)
	for i, p := range points {
		loo := append([]fitPoint(nil), points[:i]...)
		loo = append(loo, points[i+1:]...)
		la, lb, lmin, lmax, err := fitAtOffset(loo, h0)
		if err != nil {
			return nil, err
		}
		prediction := la * math.Pow(targetLevel-h0, lb)
		if math.IsNaN(prediction) || math.IsInf(prediction, 0) {
			return nil, fmt.Errorf("%w: influence_non_finite", domain.ErrGate)
		}
		parameter := math.Max(relativeChange(a, la), relativeChange(b, lb))
		predict := relativeChange(basePrediction, prediction)
		looUpper := math.Min(math.Max(lmax, historicalHigh), lmax+extensionRatio*(lmax-lmin))
		upper := math.Abs(looUpper - targetLevel)
		score := math.Max(parameter, math.Max(predict, upper/math.Max(math.Abs(targetLevel), 1e-9)))
		verdict := "pass"
		if score > influenceThreshold {
			verdict = "fail"
		}
		out = append(out, domain.InfluenceDetail{EvidenceID: p.id, ParameterChangeRatio: parameter, TargetPredictionChangeRatio: predict, UpperBoundChangeM: upper, InfluenceScore: score, Threshold: influenceThreshold, Verdict: verdict})
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].InfluenceScore == out[j].InfluenceScore {
			return out[i].EvidenceID < out[j].EvidenceID
		}
		return out[i].InfluenceScore > out[j].InfluenceScore
	})
	return out, nil
}

func floodConsensus(items []domain.Evidence, requested float64) ([]domain.FloodMarkConstraint, float64, bool, error) {
	groups := map[string][]domain.Evidence{}
	for _, x := range items {
		if x.EvidenceType == "historical_flood_mark" {
			if x.QualityDecision == "included" {
				groups[x.FloodEventID] = append(groups[x.FloodEventID], x)
			}
		}
	}
	keys := []string{}
	for k := range groups {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	out := []domain.FloodMarkConstraint{}
	for _, x := range items {
		if x.EvidenceType == "historical_flood_mark" && x.QualityDecision != "included" {
			out = append(out, domain.FloodMarkConstraint{EvidenceID: x.EvidenceID, FloodEventID: x.FloodEventID, LowerM: *x.WaterLevelM - *x.VerticalUncertaintyM, UpperM: *x.WaterLevelM + *x.VerticalUncertaintyM, Adopted: false, Reason: "quality_" + x.QualityDecision})
		}
	}
	conflict := false
	if len(groups) == 0 {
		sort.Slice(out, func(i, j int) bool { return out[i].EvidenceID < out[j].EvidenceID })
		return out, 0, false, nil
	}
	bestLo, bestHi := math.Inf(-1), math.Inf(-1)
	for _, event := range keys {
		lo, hi := math.Inf(-1), math.Inf(1)
		datum := ""
		datumConflict := false
		for _, x := range groups[event] {
			if datum == "" {
				datum = x.DatumID
			} else if datum != x.DatumID {
				conflict, datumConflict = true, true
			}
			lo = math.Max(lo, *x.WaterLevelM-*x.VerticalUncertaintyM)
			hi = math.Min(hi, *x.WaterLevelM+*x.VerticalUncertaintyM)
		}
		adopted := lo <= hi
		if datumConflict {
			adopted = false
		}
		if !adopted {
			conflict = true
		}
		for _, x := range groups[event] {
			reason := "consensus_interval"
			if datumConflict {
				reason = "datum_conflict"
			} else if !adopted {
				reason = "interval_conflict"
			}
			out = append(out, domain.FloodMarkConstraint{EvidenceID: x.EvidenceID, FloodEventID: event, LowerM: *x.WaterLevelM - *x.VerticalUncertaintyM, UpperM: *x.WaterLevelM + *x.VerticalUncertaintyM, Adopted: adopted, Reason: reason})
		}
		if adopted && hi > bestHi {
			bestLo, bestHi = lo, hi
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].EvidenceID < out[j].EvidenceID })
	if bestHi == math.Inf(-1) {
		return out, 0, conflict, nil
	}
	if requested < bestLo || requested > bestHi {
		return nil, 0, false, fmt.Errorf("%w: historical_high_m 与冻结洪痕约束摘要不一致", domain.ErrConflict)
	}
	return out, (bestLo + bestHi) / 2, conflict, nil
}

func (e *Engine) TrialProgress(c *domain.Case) domain.TrialProgress {
	return domain.TrialProgressFor(c, e.TrialMinimumSamples, e.TrialMinimumDuration, e.TrialBiasLimit)
}
func TrialBand(a *domain.Assessment, h float64) string {
	span := a.UpperBoundM - a.LowerBoundM
	if h <= a.LowerBoundM+span/3 {
		return "low"
	}
	if h > a.LowerBoundM+2*span/3 {
		return "high"
	}
	return "medium"
}
