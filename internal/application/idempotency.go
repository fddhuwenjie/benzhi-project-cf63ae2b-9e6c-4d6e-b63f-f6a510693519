package application

// idempotencyRecord identifies a replayable command response.
type idempotencyRecord struct {
	RequestID string
	Status    int
	Body      []byte
}
