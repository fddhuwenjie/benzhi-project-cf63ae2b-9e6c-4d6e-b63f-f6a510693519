package api

// envelopeMeta keeps response metadata names consistent across handlers.
type envelopeMeta struct {
	RequestID string `json:"request_id,omitempty"`
	Revision  int64  `json:"revision,omitempty"`
}
