package auditeventcachealias

import (
	"context"
	"testing"
	"time"

	"benzhi-project-cf63ae2b-9e6c-4d6e-b63f-f6a510693519/internal/application"
	"benzhi-project-cf63ae2b-9e6c-4d6e-b63f-f6a510693519/internal/audit"
	"benzhi-project-cf63ae2b-9e6c-4d6e-b63f-f6a510693519/internal/domain"
	"benzhi-project-cf63ae2b-9e6c-4d6e-b63f-f6a510693519/internal/repository"
)

func TestTimelineEventTypeFilterDoesNotCorruptCachedChain(t *testing.T) {
	ctx := context.Background()
	store, err := repository.Open(t.TempDir() + "/events.db")
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	at := time.Date(2026, 8, 1, 0, 0, 0, 0, time.UTC)
	c := &domain.Case{CaseID: "case-cache", StationID: "station-cache", RiverReach: "reach", CandidateVersion: "v1", OwnerID: "owner", State: domain.Draft, Revision: 1, CreatedAt: at}
	if err := store.Within(ctx, func(tx *repository.Tx) error {
		if err := tx.InsertCase(ctx, c); err != nil {
			return err
		}
		previous := ""
		for i, typ := range []string{"case_created", "evidence_registered", "baseline_frozen"} {
			e, err := audit.BuildEvent(c.CaseID, int64(i+1), typ, "actor", "request-"+typ, at.Add(time.Duration(i)*time.Minute), map[string]any{"type": typ}, previous)
			if err != nil {
				return err
			}
			if err := tx.AppendEvent(ctx, e); err != nil {
				return err
			}
			previous = e.EventDigest
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	svc := application.New(store)
	if _, err := svc.TimelinePage(ctx, c.CaseID, application.TimelineInput{Limit: 20}); err != nil {
		t.Fatalf("首次完整时间线不应失败: %v", err)
	}
	if _, err := svc.TimelinePage(ctx, c.CaseID, application.TimelineInput{EventType: "baseline_frozen", Limit: 20}); err != nil {
		t.Fatalf("事件类型筛选不应失败: %v", err)
	}
	if _, err := svc.TimelinePage(ctx, c.CaseID, application.TimelineInput{Limit: 20}); err != nil {
		t.Fatalf("筛选请求污染了共享审计缓存，后续完整时间线失败: %v", err)
	}
}
