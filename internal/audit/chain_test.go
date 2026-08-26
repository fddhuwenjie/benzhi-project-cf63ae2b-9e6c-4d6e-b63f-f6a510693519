package audit

import (
	"testing"
	"time"

	"benzhi-project-cf63ae2b-9e6c-4d6e-b63f-f6a510693519/internal/domain"
)

func TestAuditChainDetectsTampering(t *testing.T) {
	at := time.Date(2026, 8, 1, 0, 0, 0, 0, time.UTC)
	first, err := BuildEvent("case-a", 1, "created", "actor", "request-1", at, map[string]string{"state": "draft"}, "")
	if err != nil {
		t.Fatal(err)
	}
	second, err := BuildEvent("case-a", 2, "frozen", "actor", "request-2", at.Add(time.Second), map[string]string{"state": "baseline_frozen"}, first.EventDigest)
	if err != nil {
		t.Fatal(err)
	}
	if ok, _, message := Verify([]domain.AuditEvent{first, second}); !ok {
		t.Fatal(message)
	}
	second.PreviousDigest = "tampered"
	if ok, _, _ := Verify([]domain.AuditEvent{first, second}); ok {
		t.Fatal("摘要链篡改未被识别")
	}
}
