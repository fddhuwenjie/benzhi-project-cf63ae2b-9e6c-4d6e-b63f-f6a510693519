package assessment

import (
	"testing"
	"time"

	"benzhi-project-cf63ae2b-9e6c-4d6e-b63f-f6a510693519/internal/domain"
)

func TestCoverageUsesExplicitInstrumentBinding(t *testing.T) {
	at := time.Date(2026, 7, 1, 0, 0, 0, 0, time.UTC)
	old := domain.Qualification{QualificationID: "q-old", InstrumentID: "meter-old", InstrumentKind: "current_meter", CalibratedAt: at.Add(-48 * time.Hour), ValidUntil: at.Add(-time.Hour), UsageStartedAt: at.Add(-48 * time.Hour), UsageEndedAt: at.Add(time.Hour), Verdict: "qualified", Version: 1}
	old.Digest = domain.QualificationDigest(old)
	valid := domain.Qualification{QualificationID: "q-valid", InstrumentID: "meter-valid", InstrumentKind: "current_meter", CalibratedAt: at.Add(-48 * time.Hour), ValidUntil: at.Add(48 * time.Hour), UsageStartedAt: at.Add(-48 * time.Hour), UsageEndedAt: at.Add(time.Hour), Verdict: "qualified", Version: 1}
	valid.Digest = domain.QualificationDigest(valid)
	level := domain.Qualification{QualificationID: "q-level", InstrumentID: "level", InstrumentKind: "water_level_gauge", CalibratedAt: at.Add(-48 * time.Hour), ValidUntil: at.Add(48 * time.Hour), UsageStartedAt: at.Add(-48 * time.Hour), UsageEndedAt: at.Add(time.Hour), Verdict: "qualified", Version: 1}
	level.Digest = domain.QualificationDigest(level)
	h, q := 1.0, 10.0
	e := domain.Evidence{EvidenceID: "m1", EvidenceType: "rating_measurement", ObservedAt: at, WaterLevelM: &h, DischargeM3S: &q, InstrumentBindings: []domain.InstrumentBinding{{InstrumentKind: "current_meter", InstrumentID: old.InstrumentID, QualificationID: old.QualificationID, CertificateDigest: old.Digest}, {InstrumentKind: "water_level_gauge", InstrumentID: level.InstrumentID, QualificationID: level.QualificationID, CertificateDigest: level.Digest}}}
	m := New().CoverageMatrix([]domain.Evidence{e}, []domain.Qualification{old, valid, level})
	if m.Verdict != "fail" || m.Cells[0].IssueCode != "coverage_expired" {
		t.Fatalf("绑定的过期设备不应被同类有效设备替代: %+v", m.Cells)
	}
	e.InstrumentBindings[0] = domain.InstrumentBinding{InstrumentKind: "current_meter", InstrumentID: valid.InstrumentID, QualificationID: valid.QualificationID, CertificateDigest: valid.Digest}
	if got := New().CoverageMatrix([]domain.Evidence{e}, []domain.Qualification{old, valid, level}); got.Verdict != "pass" {
		t.Fatalf("改绑有效设备后应通过: %+v", got)
	}
}

func TestBoundedAssessmentReplayAndDifferencePath(t *testing.T) {
	c := &domain.Case{CaseID: "c", StationID: "s", RiverReach: "r", CandidateVersion: "v", OwnerID: "o", State: domain.EvidenceQualified, Revision: 3, CreatedAt: time.Now().UTC()}
	for i, x := range []struct{ h, q float64 }{{1, 10}, {2, 21}, {3, 31}, {4, 42}} {
		h, q := x.h, x.q
		e := domain.Evidence{EvidenceID: string(rune('a' + i)), EvidenceType: "rating_measurement", ObservedAt: time.Date(2026, 7, 1, i, 0, 0, 0, time.UTC), WaterLevelM: &h, DischargeM3S: &q, SourceRef: "src", ContentDigest: domain.Digest(i), QualityDecision: "included", Version: 1}
		c.Evidence = append(c.Evidence, e)
		c.EvidenceVersions = append(c.EvidenceVersions, domain.EvidenceVersion{EvidenceID: e.EvidenceID, Version: 1, ContentDigest: e.ContentDigest, Snapshot: e})
	}
	m := domain.BuildBaselineManifest(c, time.Now())
	c.BaselineManifest = &m
	c.BaselineDigest = m.Digest
	req := BoundaryRequest{RequestedLowerBoundM: .5, RequestedUpperBoundM: 4.2, MaxLowExtensionRatio: .25, MaxHighExtensionRatio: .25, HistoricalHighM: 4.2}
	run, deviations, err := New().AssessBounded(c, "run", "modeler", req, time.Now().UTC())
	if err != nil {
		t.Fatal(err)
	}
	if run.BoundaryDiagnostics[0].Verdict != "fail" || run.BoundaryDiagnostics[1].Verdict != "pass" {
		t.Fatalf("双端边界结论未独立计算: %+v", run.BoundaryDiagnostics)
	}
	foundLow := false
	for _, d := range deviations {
		if d.SourceGate == "low_extrapolation_boundary" {
			foundLow = true
		}
	}
	if !foundLow {
		t.Fatalf("低端失败未建立专属偏差: %+v", deviations)
	}
	c.Assessment = run
	c.ModelerID = "modeler"
	v, err := New().Replay(c, run)
	if err != nil || !v.Matched {
		t.Fatalf("正常运行应可重放: %+v %v", v, err)
	}
	broken := *run
	broken.ResidualMetrics = map[string]float64{}
	for k, x := range run.ResidualMetrics {
		broken.ResidualMetrics[k] = x
	}
	broken.ResidualMetrics["mean_absolute_ratio"] += .01
	v, err = New().Replay(c, &broken)
	if err != nil || v.Matched || v.DifferencePath != "residual_metrics.mean_absolute_ratio" {
		t.Fatalf("应稳定定位残差差异: %+v %v", v, err)
	}
}
