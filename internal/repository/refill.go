package repository

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"sort"
	"time"

	"benzhi-project-cf63ae2b-9e6c-4d6e-b63f-f6a510693519/internal/domain"
)

type DuplicateCandidate struct {
	CaseID   string       `json:"case_id"`
	State    domain.State `json:"state"`
	Revision int64        `json:"revision"`
}

func (t *Tx) ActiveCandidate(ctx context.Context, stationID, version string) (DuplicateCandidate, bool, error) {
	rows, err := t.tx.QueryContext(ctx, `SELECT body FROM cases WHERE station_id=? AND archived=0`, stationID)
	if err != nil {
		return DuplicateCandidate{}, false, err
	}
	defer rows.Close()
	for rows.Next() {
		var body []byte
		if err = rows.Scan(&body); err != nil {
			return DuplicateCandidate{}, false, err
		}
		var c domain.Case
		if err = json.Unmarshal(body, &c); err != nil {
			return DuplicateCandidate{}, false, err
		}
		if c.CandidateVersion == version {
			return DuplicateCandidate{c.CaseID, c.State, c.Revision}, true, nil
		}
	}
	return DuplicateCandidate{}, false, rows.Err()
}

type CaseListFilter struct {
	StationID, OwnerID string
	State              domain.State
	Archived           *bool
	Cursor             string
	Limit              int
}
type caseCursor struct {
	CreatedAt time.Time `json:"created_at"`
	CaseID    string    `json:"case_id"`
}
type CasePage struct {
	Items      []domain.CaseListItem `json:"items"`
	NextCursor string                `json:"next_cursor,omitempty"`
}

func decodeCursor(raw string) (caseCursor, error) {
	if raw == "" {
		return caseCursor{}, nil
	}
	b, err := base64.RawURLEncoding.DecodeString(raw)
	if err != nil {
		return caseCursor{}, fmt.Errorf("%w: cursor 无效", domain.ErrInvalid)
	}
	var c caseCursor
	if json.Unmarshal(b, &c) != nil || c.CreatedAt.IsZero() || c.CaseID == "" {
		return caseCursor{}, fmt.Errorf("%w: cursor 无效", domain.ErrInvalid)
	}
	return c, nil
}
func encodeCursor(c caseCursor) string {
	b, _ := json.Marshal(c)
	return base64.RawURLEncoding.EncodeToString(b)
}

func (s *Store) caseListSnapshot(ctx context.Context) ([]domain.Case, error) {
	s.caseListMu.Lock()
	defer s.caseListMu.Unlock()
	if s.caseListCached {
		return append([]domain.Case(nil), s.caseListCache...), nil
	}
	rows, err := s.db.QueryContext(ctx, `SELECT body FROM cases`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	cases := []domain.Case{}
	for rows.Next() {
		var b []byte
		if err = rows.Scan(&b); err != nil {
			return nil, err
		}
		var c domain.Case
		if err = json.Unmarshal(b, &c); err != nil {
			return nil, err
		}
		cases = append(cases, c)
	}
	if err = rows.Err(); err != nil {
		return nil, err
	}
	s.caseListCache = append([]domain.Case(nil), cases...)
	s.caseListCached = true
	return cases, nil
}

func (s *Store) ListCases(ctx context.Context, f CaseListFilter) (CasePage, error) {
	cursor, err := decodeCursor(f.Cursor)
	if err != nil {
		return CasePage{}, err
	}
	if f.Limit <= 0 {
		f.Limit = 50
	}
	if f.Limit > 200 {
		return CasePage{}, fmt.Errorf("%w: limit 不得超过 200", domain.ErrInvalid)
	}
	snapshot, err := s.caseListSnapshot(ctx)
	if err != nil {
		return CasePage{}, err
	}
	cases := make([]domain.Case, 0, len(snapshot))
	for _, c := range snapshot {
		if f.StationID != "" && c.StationID != f.StationID {
			continue
		}
		if f.OwnerID != "" && c.OwnerID != f.OwnerID {
			continue
		}
		if f.State != "" && c.State != f.State {
			continue
		}
		if f.Archived != nil && (c.State == domain.Archived) != *f.Archived {
			continue
		}
		cases = append(cases, c)
	}
	sort.Slice(cases, func(i, j int) bool {
		if cases[i].CreatedAt.Equal(cases[j].CreatedAt) {
			return cases[i].CaseID < cases[j].CaseID
		}
		return cases[i].CreatedAt.Before(cases[j].CreatedAt)
	})
	start := 0
	if !cursor.CreatedAt.IsZero() {
		start = sort.Search(len(cases), func(i int) bool {
			return cases[i].CreatedAt.After(cursor.CreatedAt) || (cases[i].CreatedAt.Equal(cursor.CreatedAt) && cases[i].CaseID > cursor.CaseID)
		})
		if start == 0 || (start > 0 && (cases[start-1].CaseID != cursor.CaseID || !cases[start-1].CreatedAt.Equal(cursor.CreatedAt))) {
			return CasePage{}, fmt.Errorf("%w: cursor 已失效", domain.ErrInvalid)
		}
	}
	end := start + f.Limit
	if end > len(cases) {
		end = len(cases)
	}
	out := CasePage{Items: []domain.CaseListItem{}}
	for i := start; i < end; i++ {
		out.Items = append(out.Items, domain.ProjectCase(&cases[i]))
	}
	if end < len(cases) && end > start {
		last := cases[end-1]
		out.NextCursor = encodeCursor(caseCursor{last.CreatedAt, last.CaseID})
	}
	return out, nil
}

type ArchiveProjection struct {
	Evidence, Qualifications, Assessments, Deviations, Decisions, Observations int
	CurveCaseID, CurveVersion                                                  string
}

func (t *Tx) ArchiveProjection(ctx context.Context, c *domain.Case) (ArchiveProjection, error) {
	p := ArchiveProjection{}
	queries := []struct {
		q string
		v *int
	}{{`SELECT COUNT(*) FROM measurement_evidence WHERE case_id=?`, &p.Evidence}, {`SELECT COUNT(*) FROM instrument_qualifications WHERE case_id=?`, &p.Qualifications}, {`SELECT COUNT(*) FROM assessment_runs WHERE case_id=?`, &p.Assessments}, {`SELECT COUNT(*) FROM deviations WHERE case_id=?`, &p.Deviations}, {`SELECT COUNT(*) FROM activation_decisions WHERE case_id=?`, &p.Decisions}, {`SELECT COUNT(*) FROM trial_observations WHERE case_id=?`, &p.Observations}}
	for _, x := range queries {
		if err := t.tx.QueryRowContext(ctx, x.q, c.CaseID).Scan(x.v); err != nil {
			return p, err
		}
	}
	err := t.tx.QueryRowContext(ctx, `SELECT case_id,curve_version FROM station_curves WHERE station_id=?`, c.StationID).Scan(&p.CurveCaseID, &p.CurveVersion)
	if err != nil {
		return p, fmt.Errorf("%w: 测站当前曲线指针不存在", domain.ErrGate)
	}
	return p, nil
}
