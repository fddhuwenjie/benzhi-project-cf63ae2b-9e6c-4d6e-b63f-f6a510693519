package domain

// Boundary describes the water-level interval in which a curve is applicable.
type Boundary struct {
	LowerM float64 `json:"lower_bound_m"`
	UpperM float64 `json:"upper_bound_m"`
}
