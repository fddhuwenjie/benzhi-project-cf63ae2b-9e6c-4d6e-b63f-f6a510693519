package timeline_invalid_cursor

import (
	"context"
	"encoding/base64"
	"testing"

	"benzhi-project-cf63ae2b-9e6c-4d6e-b63f-f6a510693519/internal/application"
	"benzhi-project-cf63ae2b-9e6c-4d6e-b63f-f6a510693519/internal/repository"
)

func TestTimelineRejectsUnknownCursor(t *testing.T) {
	ctx := context.Background()
	store, err := repository.Open(t.TempDir() + "/timeline.db")
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	svc := application.New(store)
	if _, err := svc.Create(ctx, application.CreateInput{Meta: application.Meta{RequestID: "create-1", ActorID: "owner", ExpectedRevision: 0}, CaseID: "case-1", StationID: "station-1", RiverReach: "reach", CandidateVersion: "v1", OwnerID: "owner"}); err != nil {
		t.Fatal(err)
	}
	for _, raw := range []string{"999", "-1"} {
		cursor := base64.RawURLEncoding.EncodeToString([]byte(raw))
		if _, err := svc.TimelinePage(ctx, "case-1", application.TimelineInput{Cursor: cursor, Limit: 10}); err == nil {
			t.Fatalf("TestTimelineRejectsUnknownCursor: invalid timeline cursor %q was accepted", raw)
		}
	}
}
