package audit

// archiveMember identifies a deterministic item in an authentication archive.
type archiveMember struct {
	Kind   string
	ID     string
	Digest string
}
