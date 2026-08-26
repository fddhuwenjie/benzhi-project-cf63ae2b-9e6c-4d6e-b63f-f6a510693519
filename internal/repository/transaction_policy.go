package repository

// transactionPolicy documents the all-or-nothing persistence boundary.
type transactionPolicy struct {
	WriteAuditEvent    bool
	PersistIdempotency bool
}
