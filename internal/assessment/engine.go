package assessment

import (
	"fmt"
	"math"
	"sort"
	"sync"
	"time"

	"benzhi-project-cf63ae2b-9e6c-4d6e-b63f-f6a510693519/internal/domain"
)

const MethodVersion = "rating-power-v1"

type Engine struct {
	MaxExtrapolationRatio float64
	MaxResidualRatio      float64
	TrialBiasLimit        float64
	TrialMinimumSamples   int
	TrialMinimumDuration  time.Duration
	baseCacheMu           sync.Mutex
	baseCache             map[string]assessmentBaseCacheEntry
}

type assessmentBaseCacheEntry struct {
	run        *domain.Assessment
	deviations []domain.Deviation
}

func New() *Engine {
	return &Engine{MaxExtrapolationRatio: .25, MaxResidualRatio: .15, TrialBiasLimit: .10, TrialMinimumSamples: 3, TrialMinimumDuration: 48 * time.Hour, baseCache: map[string]assessmentBaseCacheEntry{}}
}

func (e *Engine) QualifyInstrument(q *domain.Qualification) {
	q.Verdict = "qualified"
	if q.InstrumentID == "" || q.CertificateRef == "" || q.InstrumentKind == "" || q.ValidUntil.Before(q.UsageEndedAt) || q.CalibratedAt.After(q.UsageStartedAt) || q.UsageEndedAt.Before(q.UsageStartedAt) {
		q.Verdict = "unqualified"
	}
}

func (e *Engine) Assess(c *domain.Case, runID, modeler string, historicalHigh float64, maxExtension float64, now time.Time) (*domain.Assessment, []domain.Deviation, error) {
	if err := c.RequireState(domain.EvidenceQualified); err != nil {
		return nil, nil, err
	}
	cacheKey := domain.Digest(struct {
		Method           string  `json:"method"`
		HistoricalHigh   float64 `json:"historical_high_m"`
		MaximumExtension float64 `json:"maximum_extension_ratio"`
	}{MethodVersion, historicalHigh, maxExtension})
	e.baseCacheMu.Lock()
	cached, found := e.baseCache[cacheKey]
	e.baseCacheMu.Unlock()
	if found {
		c.ModelerID = modeler
		return cached.run, cached.deviations, nil
	}
	type point struct {
		id   string
		h, q float64
	}
	var points []point
	for _, item := range c.Evidence {
		if item.EvidenceType == "rating_measurement" && item.QualityDecision == "included" && item.WaterLevelM != nil && item.DischargeM3S != nil && *item.DischargeM3S > 0 {
			points = append(points, point{item.EvidenceID, *item.WaterLevelM, *item.DischargeM3S})
		}
	}
	if len(points) < 4 {
		return nil, nil, fmt.Errorf("%w: influence_insufficient_samples: 至少需要 4 个纳入测次", domain.ErrGate)
	}
	sort.Slice(points, func(i, j int) bool { return points[i].h < points[j].h })
	minH, maxH := points[0].h, points[len(points)-1].h
	if maxH-minH <= 0 {
		return nil, nil, fmt.Errorf("%w: 测次水位范围不足", domain.ErrGate)
	}
	// Deterministic log-linear power approximation Q=a*(H-h0)^b.
	h0 := minH - math.Max(.05, (maxH-minH)*.1)
	var sx, sy, sxx, sxy float64
	for _, p := range points {
		x, y := math.Log(p.h-h0), math.Log(p.q)
		sx += x
		sy += y
		sxx += x * x
		sxy += x * y
	}
	n := float64(len(points))
	denom := n*sxx - sx*sx
	if math.Abs(denom) < 1e-12 {
		return nil, nil, fmt.Errorf("%w: 拟合矩阵奇异", domain.ErrGate)
	}
	b := (n*sxy - sx*sy) / denom
	a := math.Exp((sy - b*sx) / n)
	var absSum, signedSum, squaredSum, maxAbs, lowSum, highSum float64
	var lowN, highN float64
	details := make([]domain.ResidualDetail, 0, len(points))
	type accumulator struct {
		n                        int
		signed, squared, maximum float64
	}
	bands := map[string]*accumulator{"low": {}, "medium": {}, "high": {}}
	for _, p := range points {
		predicted := a * math.Pow(p.h-h0, b)
		r := (predicted - p.q) / p.q
		band := "medium"
		if p.h <= minH+(maxH-minH)/3 {
			band = "low"
		} else if p.h > minH+2*(maxH-minH)/3 {
			band = "high"
		}
		verdict := "pass"
		if math.Abs(r) > e.MaxResidualRatio {
			verdict = "fail"
		}
		details = append(details, domain.ResidualDetail{EvidenceID: p.id, WaterLevelM: p.h, MeasuredM3S: p.q, PredictedM3S: predicted, AbsoluteResidual: math.Abs(predicted - p.q), RelativeResidual: r, Band: band, Threshold: e.MaxResidualRatio, Verdict: verdict})
		acc := bands[band]
		acc.n++
		acc.signed += r
		acc.squared += r * r
		acc.maximum = math.Max(acc.maximum, math.Abs(r))
		absSum += math.Abs(r)
		signedSum += r
		squaredSum += r * r
		maxAbs = math.Max(maxAbs, math.Abs(r))
		if p.h <= (minH+maxH)/2 {
			lowSum += r
			lowN++
		} else {
			highSum += r
			highN++
		}
	}
	mae, bias := absSum/n, signedSum/n
	rmse := math.Sqrt(squaredSum / n)
	stability := math.Sqrt(math.Max(0, squaredSum/n-bias*bias))
	constraints, consensusHigh, constraintConflict, err := floodConsensus(c.Evidence, historicalHigh)
	if err != nil {
		return nil, nil, err
	}
	if consensusHigh > 0 {
		historicalHigh = consensusHigh
	}
	requestedUpper := math.Max(maxH, historicalHigh)
	requestedRatio := (requestedUpper - maxH) / (maxH - minH)
	limit := e.MaxExtrapolationRatio
	if maxExtension > 0 && maxExtension < limit {
		limit = maxExtension
	}
	extendedUpper := math.Min(requestedUpper, maxH+limit*(maxH-minH))
	ratio := (extendedUpper - maxH) / (maxH - minH)
	verdict := "pass"
	var deviations []domain.Deviation
	if constraintConflict {
		verdict = "fail"
		deviations = append(deviations, domain.Deviation{DeviationID: runID + "-flood-constraint", SourceGate: "extrapolation_constraint", Severity: "major", State: "open"})
	}
	if mae > e.MaxResidualRatio {
		verdict = "fail"
		deviations = append(deviations, domain.Deviation{DeviationID: runID + "-residual", SourceGate: "residual_diagnostic", Severity: "major", State: "open"})
	}
	if requestedRatio > e.MaxExtrapolationRatio || (maxExtension > 0 && requestedRatio > maxExtension) {
		verdict = "fail"
		deviations = append(deviations, domain.Deviation{DeviationID: runID + "-extrapolation", SourceGate: "extrapolation_boundary", Severity: "major", State: "open"})
	}
	inputDigest := AssessmentInputDigest(c, historicalHigh, maxExtension)
	summaries := make([]domain.ResidualBandSummary, 0, 3)
	for _, band := range []string{"low", "medium", "high"} {
		acc := bands[band]
		n := math.Max(1, float64(acc.n))
		bias := acc.signed / n
		rmseBand := math.Sqrt(acc.squared / n)
		verdict := "pass"
		if math.Abs(bias) > e.MaxResidualRatio || rmseBand > e.MaxResidualRatio || acc.maximum > e.MaxResidualRatio {
			verdict = "fail"
		}
		check := func(v float64) domain.MetricCheck {
			x := "pass"
			if math.Abs(v) > e.MaxResidualRatio {
				x = "fail"
			}
			return domain.MetricCheck{Actual: v, Threshold: e.MaxResidualRatio, Verdict: x}
		}
		summaries = append(summaries, domain.ResidualBandSummary{Band: band, SampleCount: acc.n, SignedBias: check(bias), RMSE: check(rmseBand), MaximumAbs: check(acc.maximum), Verdict: verdict})
	}
	sort.Slice(details, func(i, j int) bool { return details[i].EvidenceID < details[j].EvidenceID })
	influence, err := leaveOneOut(c.Evidence, a, b, h0, extendedUpper, historicalHigh)
	if err != nil {
		return nil, nil, err
	}
	for _, d := range influence {
		if d.Verdict == "fail" {
			verdict = "fail"
			deviations = append(deviations, domain.Deviation{DeviationID: runID + "-influence-" + d.EvidenceID, SourceGate: "influence_diagnostic", Severity: "major", State: "open"})
		}
	}
	run := &domain.Assessment{RunID: runID, InputDigest: inputDigest, MethodVersion: MethodVersion, Parameters: map[string]float64{"a": a, "b": b, "h0": h0, "historical_high_m": historicalHigh, "maximum_extension_ratio": maxExtension}, ResidualMetrics: map[string]float64{"mean_absolute_ratio": mae, "root_mean_square_ratio": rmse, "maximum_absolute_ratio": maxAbs, "signed_bias": bias, "residual_stability": stability, "low_band_bias": lowSum / math.Max(1, lowN), "high_band_bias": highSum / math.Max(1, highN), "low_extrapolation_ratio": 0, "high_extrapolation_ratio": ratio}, LowerBoundM: minH, UpperBoundM: extendedUpper, ExtrapolationRatio: ratio, Verdict: verdict, CompletedAt: now, ResidualDetails: details, BandSummaries: summaries, InfluenceDetails: influence, FloodMarkConstraints: constraints}
	e.baseCacheMu.Lock()
	e.baseCache[cacheKey] = assessmentBaseCacheEntry{run: run, deviations: deviations}
	e.baseCacheMu.Unlock()
	c.ModelerID = modeler
	return run, deviations, nil
}

func AssessmentInputDigest(c *domain.Case, historicalHigh, maxExtension float64) string {
	return domain.Digest(struct {
		Baseline         string  `json:"baseline_digest"`
		Method           string  `json:"method_version"`
		HistoricalHigh   float64 `json:"historical_high_m"`
		MaximumExtension float64 `json:"maximum_extension_ratio"`
	}{c.BaselineDigest, MethodVersion, historicalHigh, maxExtension})
}

func (e *Engine) TrialObservation(id string, observed time.Time, h, measured, predicted float64) (domain.TrialObservation, error) {
	if measured <= 0 || predicted <= 0 {
		return domain.TrialObservation{}, fmt.Errorf("%w: 流量必须大于零", domain.ErrInvalid)
	}
	bias := (predicted - measured) / measured
	verdict := "continue"
	if math.Abs(bias) > e.TrialBiasLimit {
		verdict = "suspend"
	}
	return domain.TrialObservation{ObservationID: id, ObservedAt: observed, WaterLevelM: h, MeasuredDischargeM3S: measured, PredictedDischargeM3S: predicted, RelativeBias: bias, Verdict: verdict}, nil
}

func (e *Engine) TrialQualified(items []domain.TrialObservation) bool {
	if len(items) < e.TrialMinimumSamples {
		return false
	}
	for _, o := range items {
		if o.Verdict != "continue" {
			return false
		}
	}
	ordered := append([]domain.TrialObservation(nil), items...)
	sort.Slice(ordered, func(i, j int) bool { return ordered[i].ObservedAt.Before(ordered[j].ObservedAt) })
	return ordered[len(ordered)-1].ObservedAt.Sub(ordered[0].ObservedAt) >= e.TrialMinimumDuration
}

func (e *Engine) RetestThreshold(sourceGate string, requested float64) (float64, error) {
	switch sourceGate {
	case "residual_diagnostic":
		return e.MaxResidualRatio, nil
	case "extrapolation_boundary", "extrapolation_constraint", "low_extrapolation_boundary", "high_extrapolation_boundary":
		return e.MaxExtrapolationRatio, nil
	case "influence_diagnostic":
		return influenceThreshold, nil
	case "instrument_certificate_invalidated":
		return 0, nil
	default:
		if requested <= 0 || math.IsNaN(requested) || math.IsInf(requested, 0) {
			return 0, fmt.Errorf("%w: source_gate %s 缺少有效复验门槛", domain.ErrInvalid, sourceGate)
		}
		return requested, nil
	}
}
