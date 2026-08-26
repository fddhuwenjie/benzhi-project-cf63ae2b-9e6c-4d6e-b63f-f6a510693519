package repository

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"time"

	"benzhi-project-cf63ae2b-9e6c-4d6e-b63f-f6a510693519/internal/audit"
	"benzhi-project-cf63ae2b-9e6c-4d6e-b63f-f6a510693519/internal/domain"
)

type IntegrityReport struct {
	CheckedAt         time.Time `json:"checked_at"`
	CaseCount         int       `json:"case_count"`
	EventCount        int       `json:"event_count"`
	ArchiveCount      int       `json:"archive_count"`
	ActiveTrialCount  int       `json:"active_trial_count"`
	CurrentCurveCount int       `json:"current_curve_count"`
	Verdict           string    `json:"verdict"`
}

func (s *Store) CheckIntegrity(ctx context.Context) (IntegrityReport, error) {
	report := IntegrityReport{CheckedAt: time.Now().UTC(), Verdict: "pass"}
	var sqliteVerdict string
	if err := s.db.QueryRowContext(ctx, `PRAGMA integrity_check`).Scan(&sqliteVerdict); err != nil {
		return report, err
	}
	if sqliteVerdict != "ok" {
		return report, fmt.Errorf("SQLite 完整性检查失败: %s", sqliteVerdict)
	}
	foreignRows, err := s.db.QueryContext(ctx, `PRAGMA foreign_key_check`)
	if err != nil {
		return report, err
	}
	if foreignRows.Next() {
		foreignRows.Close()
		return report, fmt.Errorf("SQLite 外键完整性检查失败")
	}
	if err := foreignRows.Close(); err != nil {
		return report, err
	}
	rows, err := s.db.QueryContext(ctx, `SELECT body FROM cases ORDER BY case_id`)
	if err != nil {
		return report, err
	}
	var cases []domain.Case
	for rows.Next() {
		var body []byte
		var c domain.Case
		if err = rows.Scan(&body); err != nil {
			rows.Close()
			return report, err
		}
		if err = json.Unmarshal(body, &c); err != nil {
			rows.Close()
			return report, err
		}
		if err = c.ValidateConsistency(); err != nil {
			rows.Close()
			return report, fmt.Errorf("案件 %s 快照不一致: %w", c.CaseID, err)
		}
		cases = append(cases, c)
	}
	if err = rows.Close(); err != nil {
		return report, err
	}
	report.CaseCount = len(cases)
	for _, c := range cases {
		events, err := s.Events(ctx, c.CaseID)
		if err != nil {
			return report, err
		}
		if len(events) == 0 {
			return report, fmt.Errorf("案件 %s 缺少审计事件", c.CaseID)
		}
		ok, position, message := audit.Verify(events)
		if !ok {
			return report, fmt.Errorf("案件 %s 审计链第 %d 项无效: %s", c.CaseID, position, message)
		}
		report.EventCount += len(events)
		if c.State == domain.Archived {
			archived, err := s.Archive(ctx, c.CaseID)
			if err != nil {
				return report, fmt.Errorf("归档案件 %s 缺少档案: %w", c.CaseID, err)
			}
			if ok, message := audit.VerifyArchive(archived); !ok {
				return report, fmt.Errorf("归档案件 %s 无效: %s", c.CaseID, message)
			}
			report.ArchiveCount++
		}
	}
	if err = countRow(ctx, s.db, `SELECT COUNT(*) FROM active_trials`, &report.ActiveTrialCount); err != nil {
		return report, err
	}
	if err = countRow(ctx, s.db, `SELECT COUNT(*) FROM station_curves`, &report.CurrentCurveCount); err != nil {
		return report, err
	}
	return report, nil
}

func countRow(ctx context.Context, db *sql.DB, query string, target *int) error {
	return db.QueryRowContext(ctx, query).Scan(target)
}

type CaseProjection struct {
	CaseID                string       `json:"case_id"`
	StationID             string       `json:"station_id"`
	State                 domain.State `json:"state"`
	Revision              int64        `json:"revision"`
	EvidenceCount         int          `json:"evidence_count"`
	OpenDeviationCount    int          `json:"open_deviation_count"`
	TrialObservationCount int          `json:"trial_observation_count"`
	UpdatedSequence       int64        `json:"updated_sequence"`
}

func (s *Store) CaseProjection(ctx context.Context, caseID string) (CaseProjection, error) {
	var p CaseProjection
	err := s.db.QueryRowContext(ctx, `SELECT c.case_id,c.station_id,c.state,c.revision,(SELECT COUNT(*) FROM measurement_evidence e WHERE e.case_id=c.case_id),(SELECT COUNT(*) FROM deviations d WHERE d.case_id=c.case_id AND d.state<>'verified'),(SELECT COUNT(*) FROM trial_observations o WHERE o.case_id=c.case_id),(SELECT COALESCE(MAX(sequence_no),0) FROM audit_events a WHERE a.case_id=c.case_id) FROM cases c WHERE c.case_id=?`, caseID).Scan(&p.CaseID, &p.StationID, &p.State, &p.Revision, &p.EvidenceCount, &p.OpenDeviationCount, &p.TrialObservationCount, &p.UpdatedSequence)
	if err == sql.ErrNoRows {
		return p, domain.ErrNotFound
	}
	return p, err
}
