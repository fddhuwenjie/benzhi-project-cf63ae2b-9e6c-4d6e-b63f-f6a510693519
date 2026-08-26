package api

// pageOptions is shared by read-only list endpoints.
type pageOptions struct {
	Cursor string
	Limit  int
}
