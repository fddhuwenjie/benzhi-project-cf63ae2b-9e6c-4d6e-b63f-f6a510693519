package assessment

import (
	"bytes"
	"encoding/json"
	"fmt"
	"math"
	"sort"
	"time"

	"benzhi-project-cf63ae2b-9e6c-4d6e-b63f-f6a510693519/internal/domain"
)

type BoundaryRequest struct {
	RequestedLowerBoundM  float64
	RequestedUpperBoundM  float64
	MaxLowExtensionRatio  float64
	MaxHighExtensionRatio float64
	HistoricalHighM       float64
}

func (e *Engine) AssessBounded(c *domain.Case, runID, modeler string, req BoundaryRequest, now time.Time) (*domain.Assessment, []domain.Deviation, error) {
	if err := domain.ValidateBoundaryRequest(req.RequestedLowerBoundM, req.RequestedUpperBoundM, req.MaxLowExtensionRatio, req.MaxHighExtensionRatio); err != nil {
		return nil, nil, err
	}
	run, deviations, err := e.Assess(c, runID, modeler, req.HistoricalHighM, req.MaxHighExtensionRatio, now)
	if err != nil {
		return nil, nil, err
	}
	minH, maxH := math.Inf(1), math.Inf(-1)
	for _, d := range run.ResidualDetails {
		minH = math.Min(minH, d.WaterLevelM)
		maxH = math.Max(maxH, d.WaterLevelM)
	}
	span := maxH - minH
	h0 := run.Parameters["h0"]
	margin := math.Max(.01, span*.02)
	lowLimit := math.Min(req.MaxLowExtensionRatio, e.MaxExtrapolationRatio)
	highLimit := math.Min(req.MaxHighExtensionRatio, e.MaxExtrapolationRatio)
	lowAllowedByRatio := minH - lowLimit*span
	lowAllowedByControl := h0 + margin
	allowedLow, lowSource := lowAllowedByRatio, "maximum_extension_ratio"
	if lowAllowedByControl > allowedLow {
		allowedLow, lowSource = lowAllowedByControl, "control_level_safety_margin"
	}
	adoptedLow := math.Max(req.RequestedLowerBoundM, allowedLow)
	lowVerdict := "pass"
	if req.RequestedLowerBoundM < allowedLow {
		lowVerdict = "fail"
	}
	predicted := run.Parameters["a"] * math.Pow(adoptedLow-h0, run.Parameters["b"])
	if math.IsNaN(predicted) || math.IsInf(predicted, 0) || predicted < 0 {
		lowVerdict = "fail"
		adoptedLow = minH
		allowedLow = minH
		lowSource = "finite_nonnegative_prediction"
	}
	allowedHigh := maxH + highLimit*span
	highSource := "maximum_extension_ratio"
	historicalConstraint := run.Parameters["historical_high_m"]
	if historicalConstraint > maxH && historicalConstraint < allowedHigh {
		allowedHigh = historicalConstraint
		highSource = "historical_flood_consensus"
	}
	adoptedHigh := math.Min(req.RequestedUpperBoundM, allowedHigh)
	highVerdict := "pass"
	if req.RequestedUpperBoundM > allowedHigh {
		highVerdict = "fail"
	}
	run.RequestedLowerBoundM, run.RequestedUpperBoundM = req.RequestedLowerBoundM, req.RequestedUpperBoundM
	run.MaxLowExtensionRatio, run.MaxHighExtensionRatio = req.MaxLowExtensionRatio, req.MaxHighExtensionRatio
	run.LowerBoundM, run.UpperBoundM = adoptedLow, adoptedHigh
	run.ExtrapolationRatio = math.Max((minH-adoptedLow)/span, (adoptedHigh-maxH)/span)
	run.ResidualMetrics["low_extrapolation_ratio"] = (minH - adoptedLow) / span
	run.ResidualMetrics["high_extrapolation_ratio"] = (adoptedHigh - maxH) / span
	run.BoundaryDiagnostics = []domain.BoundaryDiagnostic{
		{Side: "low", RequestedBoundM: req.RequestedLowerBoundM, AllowedBoundM: allowedLow, AdoptedBoundM: adoptedLow, ExtrapolationRatio: (minH - adoptedLow) / span, LimitSource: lowSource, ExceedanceM: math.Max(0, allowedLow-req.RequestedLowerBoundM), Verdict: lowVerdict},
		{Side: "high", RequestedBoundM: req.RequestedUpperBoundM, AllowedBoundM: allowedHigh, AdoptedBoundM: adoptedHigh, ExtrapolationRatio: (adoptedHigh - maxH) / span, LimitSource: highSource, ExceedanceM: math.Max(0, req.RequestedUpperBoundM-allowedHigh), Verdict: highVerdict},
	}
	filtered := deviations[:0]
	for _, d := range deviations {
		if d.SourceGate != "extrapolation_boundary" {
			filtered = append(filtered, d)
		}
	}
	deviations = filtered
	if lowVerdict == "fail" {
		run.Verdict = "fail"
		deviations = append(deviations, domain.Deviation{DeviationID: runID + "-low-extrapolation", SourceGate: "low_extrapolation_boundary", Severity: "major", State: "open"})
	}
	if highVerdict == "fail" {
		run.Verdict = "fail"
		deviations = append(deviations, domain.Deviation{DeviationID: runID + "-high-extrapolation", SourceGate: "high_extrapolation_boundary", Severity: "major", State: "open"})
	}
	run.InputDigest = AssessmentInputDigestV40(c, req)
	return run, deviations, nil
}

func AssessmentInputDigestV40(c *domain.Case, req BoundaryRequest) string {
	return domain.Digest(struct {
		Baseline, Method string
		Request          BoundaryRequest
	}{c.BaselineDigest, MethodVersion, req})
}

type ReplayVerification struct {
	Matched            bool               `json:"matched"`
	InputDigest        string             `json:"input_digest"`
	MethodVersion      string             `json:"method_version"`
	DifferencePath     string             `json:"difference_path,omitempty"`
	SavedValue         any                `json:"saved_value,omitempty"`
	ReplayedValue      any                `json:"replayed_value,omitempty"`
	NumericTolerance   float64            `json:"numeric_tolerance"`
	UnreplayableReason string             `json:"unreplayable_reason,omitempty"`
	Replayed           *domain.Assessment `json:"-"`
}

func (e *Engine) Replay(c *domain.Case, saved *domain.Assessment) (ReplayVerification, error) {
	out := ReplayVerification{InputDigest: saved.InputDigest, MethodVersion: saved.MethodVersion, NumericTolerance: 1e-9}
	if saved.MethodVersion != MethodVersion {
		out.UnreplayableReason = "method_version_unavailable"
		return out, nil
	}
	evidence, err := c.FrozenEvidence()
	if err != nil {
		out.UnreplayableReason = "baseline_evidence_missing_or_mismatched"
		return out, nil
	}
	copyCase := *c
	copyCase.State = domain.EvidenceQualified
	copyCase.Evidence = evidence
	copyCase.Assessment = nil
	req := BoundaryRequest{saved.RequestedLowerBoundM, saved.RequestedUpperBoundM, saved.MaxLowExtensionRatio, saved.MaxHighExtensionRatio, saved.Parameters["historical_high_m"]}
	if AssessmentInputDigestV40(&copyCase, req) != saved.InputDigest {
		out.UnreplayableReason = "input_digest_mismatch"
		return out, nil
	}
	replayed, _, err := e.AssessBounded(&copyCase, saved.RunID, c.ModelerID, req, saved.CompletedAt)
	if err != nil {
		return out, err
	}
	out.Replayed = replayed
	path, a, b, match := compareAssessment(saved, replayed, out.NumericTolerance)
	out.Matched = match
	out.DifferencePath = path
	out.SavedValue = a
	out.ReplayedValue = b
	return out, nil
}

func compareAssessment(a, b *domain.Assessment, tol float64) (string, any, any, bool) {
	decode := func(v any) any {
		raw, _ := json.Marshal(v)
		dec := json.NewDecoder(bytes.NewReader(raw))
		dec.UseNumber()
		var out any
		_ = dec.Decode(&out)
		return out
	}
	return compareReplayValue("", decode(a), decode(b), tol)
}

func compareReplayValue(path string, a, b any, tol float64) (string, any, any, bool) {
	switch x := a.(type) {
	case map[string]any:
		y, ok := b.(map[string]any)
		if !ok {
			return path, a, b, false
		}
		keys := make([]string, 0, len(x))
		for k := range x {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		if len(x) != len(y) {
			return path + ".length", len(x), len(y), false
		}
		for _, k := range keys {
			next := k
			if path != "" {
				next = path + "." + k
			}
			yv, exists := y[k]
			if !exists {
				return next, x[k], nil, false
			}
			if p, av, bv, ok := compareReplayValue(next, x[k], yv, tol); !ok {
				return p, av, bv, false
			}
		}
	case []any:
		y, ok := b.([]any)
		if !ok {
			return path, a, b, false
		}
		if len(x) != len(y) {
			return path + ".length", len(x), len(y), false
		}
		for i := range x {
			next := fmt.Sprintf("%s[%d]", path, i)
			if p, av, bv, ok := compareReplayValue(next, x[i], y[i], tol); !ok {
				return p, av, bv, false
			}
		}
	case json.Number:
		y, ok := b.(json.Number)
		if !ok {
			return path, a, b, false
		}
		xf, _ := x.Float64()
		yf, _ := y.Float64()
		if math.Abs(xf-yf) > tol {
			return path, xf, yf, false
		}
	default:
		if fmt.Sprint(a) != fmt.Sprint(b) {
			return path, a, b, false
		}
	}
	return "", nil, nil, true
}
