package assessment

import (
	"math"
	"testing"
	"time"

	"benzhi-project-cf63ae2b-9e6c-4d6e-b63f-f6a510693519/internal/domain"
)

func TestInstrumentCoverage(t *testing.T) {
	e := New()
	q := domain.Qualification{InstrumentID: "i", InstrumentKind: "current_meter", CertificateRef: "cert", CalibratedAt: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC), ValidUntil: time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC), UsageStartedAt: time.Date(2026, 7, 1, 0, 0, 0, 0, time.UTC), UsageEndedAt: time.Date(2026, 7, 2, 0, 0, 0, 0, time.UTC)}
	e.QualifyInstrument(&q)
	if q.Verdict != "unqualified" {
		t.Fatalf("过期证书不应合格: %s", q.Verdict)
	}
}

func TestTrialBiasThreshold(t *testing.T) {
	e := New()
	o, err := e.TrialObservation("o", time.Now(), 2, 100, 112)
	if err != nil {
		t.Fatal(err)
	}
	if o.Verdict != "suspend" || math.Abs(o.RelativeBias-.12) > 1e-9 {
		t.Fatalf("偏差判断错误: %#v", o)
	}
}
