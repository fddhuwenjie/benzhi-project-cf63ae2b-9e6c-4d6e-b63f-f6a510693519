package archive_snapshot_slice_alias_test

import (
	"testing"
	"time"

	"benzhi-project-cf63ae2b-9e6c-4d6e-b63f-f6a510693519/internal/audit"
	"benzhi-project-cf63ae2b-9e6c-4d6e-b63f-f6a510693519/internal/domain"
)

func TestBuildArchiveDoesNotReorderSourceAggregate(t *testing.T) {
	source := domain.Case{
		CaseID: "case-archive-alias",
		Evidence: []domain.Evidence{
			{EvidenceID: "evidence-z"},
			{EvidenceID: "evidence-a"},
		},
		Decisions: []domain.Decision{
			{DecisionID: "decision-z"},
			{DecisionID: "decision-a"},
		},
	}

	archive, err := audit.BuildArchive(source, nil, time.Date(2026, 8, 27, 12, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatalf("BuildArchive 返回错误: %v", err)
	}
	if archive.Case.Evidence[0].EvidenceID != "evidence-a" || archive.Case.Decisions[0].DecisionID != "decision-a" {
		t.Fatal("归档快照没有按稳定标识排序，测试前提不成立")
	}
	if source.Evidence[0].EvidenceID != "evidence-z" {
		t.Errorf("BuildArchive 污染来源案件的 Evidence 顺序: got %q", source.Evidence[0].EvidenceID)
	}
	if source.Decisions[0].DecisionID != "decision-z" {
		t.Errorf("BuildArchive 污染来源案件的 Decisions 顺序: got %q", source.Decisions[0].DecisionID)
	}
}
