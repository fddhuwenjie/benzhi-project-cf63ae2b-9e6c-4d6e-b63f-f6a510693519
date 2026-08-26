package domain

import (
	"errors"
	"testing"
	"time"
)

func TestStateMachineRejectsSkippedState(t *testing.T) {
	c := &Case{State: Draft}
	if err := c.Advance(EvidenceQualified); !errors.Is(err, ErrGate) {
		t.Fatalf("期望非法转换错误，实际 %v", err)
	}
	if err := c.Advance(BaselineFrozen); err != nil {
		t.Fatal(err)
	}
}

func TestQualifyEvidenceRejectsSilentDuplicate(t *testing.T) {
	h, q := 1.2, 12.0
	at := time.Date(2026, 7, 1, 0, 0, 0, 0, time.UTC)
	c := &Case{State: BaselineFrozen, Evidence: []Evidence{
		{EvidenceID: "a", EvidenceType: "rating_measurement", ObservedAt: at, WaterLevelM: &h, DischargeM3S: &q, QualityDecision: "included"},
		{EvidenceID: "b", EvidenceType: "rating_measurement", ObservedAt: at, WaterLevelM: &h, DischargeM3S: &q, QualityDecision: "included"},
	}}
	if err := c.QualifyEvidence(); !errors.Is(err, ErrGate) {
		t.Fatalf("重复测次应被门禁拒绝，实际 %v", err)
	}
}

func TestArchivedCaseIsReadOnly(t *testing.T) {
	c := &Case{State: Archived}
	if !errors.Is(c.EnsureMutable(), ErrArchived) {
		t.Fatal("归档案件必须只读")
	}
}
