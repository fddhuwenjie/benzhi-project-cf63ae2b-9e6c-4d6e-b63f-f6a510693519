package curvehistorycachestale_test

import (
	"context"
	"testing"
	"time"

	"benzhi-project-cf63ae2b-9e6c-4d6e-b63f-f6a510693519/internal/domain"
	"benzhi-project-cf63ae2b-9e6c-4d6e-b63f-f6a510693519/internal/repository"
)

func TestCurveHistoryRefreshesAfterActivation(t *testing.T) {
	ctx := context.Background()
	store, err := repository.Open(t.TempDir() + "/cache.db")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })

	before, err := store.CurveHistory(ctx, "station-cache-edge")
	if err != nil {
		t.Fatal(err)
	}
	if len(before) != 0 {
		t.Fatalf("测试前置状态异常: %+v", before)
	}

	effective := time.Date(2026, 8, 26, 3, 4, 5, 0, time.UTC)
	decision := domain.Decision{
		DecisionID:        "activate-cache-edge",
		DecisionType:      "activation",
		CurveVersion:      "curve-v1",
		AuthorizedBy:      "authority",
		EffectiveFrom:     effective,
		LowerBoundM:       1,
		UpperBoundM:       5,
		RollbackCondition: "偏差超限时回退",
	}
	c := &domain.Case{
		CaseID:           "case-cache-edge",
		StationID:        "station-cache-edge",
		CandidateVersion: "curve-v1",
		Decisions:        []domain.Decision{decision},
	}
	emptyPointerDigest := domain.Digest(map[string]string{})
	if err = store.Within(ctx, func(tx *repository.Tx) error {
		return tx.Activate(ctx, c, decision, emptyPointerDigest)
	}); err != nil {
		t.Fatal(err)
	}

	pointer, err := store.CurvePointer(ctx, c.StationID)
	if err != nil {
		t.Fatal(err)
	}
	if pointer["curve_version"] != "curve-v1" {
		t.Fatalf("启用事务未更新当前指针: %+v", pointer)
	}

	after, err := store.CurveHistory(ctx, c.StationID)
	if err != nil {
		t.Fatal(err)
	}
	if len(after) != 1 || after[0]["curve_version"] != "curve-v1" {
		t.Fatalf("正式启用后曲线历史仍为陈旧缓存: %+v", after)
	}
}
