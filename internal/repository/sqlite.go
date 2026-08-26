package repository

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"time"

	_ "modernc.org/sqlite"

	"benzhi-project-cf63ae2b-9e6c-4d6e-b63f-f6a510693519/internal/audit"
	"benzhi-project-cf63ae2b-9e6c-4d6e-b63f-f6a510693519/internal/domain"
)

type Store struct {
	db             *sql.DB
	caseListMu     sync.Mutex
	caseListCache  []domain.Case
	caseListCached bool
}
type Tx struct{ tx *sql.Tx }

func Open(path string) (*Store, error) {
	if path == "" {
		path = "curve-certification.db"
	}
	db, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	s := &Store{db: db}
	if err = s.migrate(context.Background()); err != nil {
		db.Close()
		return nil, err
	}
	if _, err = s.CheckIntegrity(context.Background()); err != nil {
		db.Close()
		return nil, fmt.Errorf("启动完整性检查失败: %w", err)
	}
	return s, nil
}
func (s *Store) Close() error                   { return s.db.Close() }
func (s *Store) Ping(ctx context.Context) error { return s.db.PingContext(ctx) }

func (s *Store) migrate(ctx context.Context) error {
	stmts := []string{
		`PRAGMA journal_mode=WAL`, `PRAGMA foreign_keys=ON`, `PRAGMA busy_timeout=5000`,
		`CREATE TABLE IF NOT EXISTS cases(case_id TEXT PRIMARY KEY,station_id TEXT NOT NULL,revision INTEGER NOT NULL,state TEXT NOT NULL,body BLOB NOT NULL,archived INTEGER NOT NULL DEFAULT 0)`,
		`CREATE TABLE IF NOT EXISTS idempotency(request_id TEXT PRIMARY KEY,case_id TEXT NOT NULL,status INTEGER NOT NULL,response BLOB NOT NULL,created_at TEXT NOT NULL)`,
		`CREATE TABLE IF NOT EXISTS idempotency_commands(request_id TEXT PRIMARY KEY REFERENCES idempotency(request_id),command_type TEXT NOT NULL)`,
		`CREATE TABLE IF NOT EXISTS audit_events(case_id TEXT NOT NULL,sequence_no INTEGER NOT NULL,event_digest TEXT NOT NULL,body BLOB NOT NULL,PRIMARY KEY(case_id,sequence_no),UNIQUE(event_digest))`,
		`CREATE TABLE IF NOT EXISTS archives(case_id TEXT PRIMARY KEY,digest TEXT NOT NULL,body BLOB NOT NULL,created_at TEXT NOT NULL)`,
		`CREATE TABLE IF NOT EXISTS station_curves(station_id TEXT PRIMARY KEY,case_id TEXT NOT NULL,curve_version TEXT NOT NULL,effective_from TEXT NOT NULL,replaces_version TEXT NOT NULL DEFAULT '')`,
		`CREATE TABLE IF NOT EXISTS station_curve_history(station_id TEXT NOT NULL,case_id TEXT NOT NULL,curve_version TEXT NOT NULL,effective_from TEXT NOT NULL,effective_until TEXT,decision_id TEXT NOT NULL,replaces_version TEXT NOT NULL DEFAULT '',PRIMARY KEY(station_id,curve_version),UNIQUE(station_id,effective_from))`,
		`CREATE TABLE IF NOT EXISTS active_trials(station_id TEXT PRIMARY KEY,case_id TEXT NOT NULL UNIQUE,curve_version TEXT NOT NULL,effective_from TEXT NOT NULL,effective_until TEXT NOT NULL)`,
		`CREATE TABLE IF NOT EXISTS measurement_evidence(evidence_id TEXT PRIMARY KEY,case_id TEXT NOT NULL REFERENCES cases(case_id),evidence_type TEXT NOT NULL,observed_at TEXT NOT NULL,water_level_m REAL,discharge_m3s REAL,source_ref TEXT NOT NULL,content_digest TEXT NOT NULL,quality_decision TEXT NOT NULL,decision_reason TEXT NOT NULL)`,
		`CREATE TABLE IF NOT EXISTS evidence_instrument_bindings(case_id TEXT NOT NULL REFERENCES cases(case_id),evidence_id TEXT NOT NULL,instrument_kind TEXT NOT NULL,instrument_id TEXT NOT NULL,qualification_id TEXT NOT NULL,certificate_digest TEXT NOT NULL,PRIMARY KEY(case_id,evidence_id,instrument_kind))`,
		`CREATE TABLE IF NOT EXISTS instrument_qualifications(qualification_id TEXT PRIMARY KEY,case_id TEXT NOT NULL REFERENCES cases(case_id),instrument_id TEXT NOT NULL,instrument_kind TEXT NOT NULL,certificate_ref TEXT NOT NULL,calibrated_at TEXT NOT NULL,valid_until TEXT NOT NULL,usage_started_at TEXT NOT NULL,usage_ended_at TEXT NOT NULL,verdict TEXT NOT NULL)`,
		`CREATE TABLE IF NOT EXISTS instrument_qualification_versions(case_id TEXT NOT NULL REFERENCES cases(case_id),qualification_id TEXT NOT NULL,version INTEGER NOT NULL,digest TEXT NOT NULL,previous_digest TEXT NOT NULL,body BLOB NOT NULL,PRIMARY KEY(case_id,qualification_id,version))`,
		`CREATE TABLE IF NOT EXISTS assessment_runs(run_id TEXT PRIMARY KEY,case_id TEXT NOT NULL REFERENCES cases(case_id),input_digest TEXT NOT NULL,method_version TEXT NOT NULL,parameters_json BLOB NOT NULL,residual_metrics_json BLOB NOT NULL,lower_bound_m REAL NOT NULL,upper_bound_m REAL NOT NULL,extrapolation_ratio REAL NOT NULL,verdict TEXT NOT NULL,completed_at TEXT NOT NULL)`,
		`CREATE TABLE IF NOT EXISTS assessment_run_details(run_id TEXT PRIMARY KEY,case_id TEXT NOT NULL REFERENCES cases(case_id),body BLOB NOT NULL)`,
		`CREATE TABLE IF NOT EXISTS deviations(deviation_id TEXT PRIMARY KEY,case_id TEXT NOT NULL REFERENCES cases(case_id),source_gate TEXT NOT NULL,severity TEXT NOT NULL,state TEXT NOT NULL,body BLOB NOT NULL)`,
		`CREATE TABLE IF NOT EXISTS activation_decisions(decision_id TEXT PRIMARY KEY,case_id TEXT NOT NULL REFERENCES cases(case_id),decision_type TEXT NOT NULL,curve_version TEXT NOT NULL,authorized_by TEXT NOT NULL,effective_from TEXT NOT NULL,effective_until TEXT,lower_bound_m REAL NOT NULL,upper_bound_m REAL NOT NULL,rollback_condition TEXT NOT NULL,replaces_version TEXT NOT NULL)`,
		`CREATE TABLE IF NOT EXISTS trial_observations(observation_id TEXT PRIMARY KEY,case_id TEXT NOT NULL REFERENCES cases(case_id),observed_at TEXT NOT NULL,water_level_m REAL NOT NULL,measured_discharge_m3s REAL NOT NULL,predicted_discharge_m3s REAL NOT NULL,relative_bias REAL NOT NULL,verdict TEXT NOT NULL)`,
		`CREATE TRIGGER IF NOT EXISTS prevent_archived_case_update BEFORE UPDATE ON cases WHEN OLD.archived=1 BEGIN SELECT RAISE(ABORT,'archived case is immutable'); END`,
		`CREATE TRIGGER IF NOT EXISTS prevent_archived_case_delete BEFORE DELETE ON cases WHEN OLD.archived=1 BEGIN SELECT RAISE(ABORT,'archived case is immutable'); END`,
		`CREATE TRIGGER IF NOT EXISTS prevent_archived_evidence_update BEFORE UPDATE ON measurement_evidence WHEN (SELECT archived FROM cases WHERE case_id=OLD.case_id)=1 BEGIN SELECT RAISE(ABORT,'archived case is immutable'); END`,
		`CREATE TRIGGER IF NOT EXISTS prevent_archived_evidence_delete BEFORE DELETE ON measurement_evidence WHEN (SELECT archived FROM cases WHERE case_id=OLD.case_id)=1 BEGIN SELECT RAISE(ABORT,'archived case is immutable'); END`,
		`CREATE TRIGGER IF NOT EXISTS prevent_archived_binding_update BEFORE UPDATE ON evidence_instrument_bindings WHEN (SELECT archived FROM cases WHERE case_id=OLD.case_id)=1 BEGIN SELECT RAISE(ABORT,'archived case is immutable'); END`,
		`CREATE TRIGGER IF NOT EXISTS prevent_archived_binding_delete BEFORE DELETE ON evidence_instrument_bindings WHEN (SELECT archived FROM cases WHERE case_id=OLD.case_id)=1 BEGIN SELECT RAISE(ABORT,'archived case is immutable'); END`,
		`CREATE TRIGGER IF NOT EXISTS prevent_archived_qualification_update BEFORE UPDATE ON instrument_qualifications WHEN (SELECT archived FROM cases WHERE case_id=OLD.case_id)=1 BEGIN SELECT RAISE(ABORT,'archived case is immutable'); END`,
		`CREATE TRIGGER IF NOT EXISTS prevent_archived_qualification_delete BEFORE DELETE ON instrument_qualifications WHEN (SELECT archived FROM cases WHERE case_id=OLD.case_id)=1 BEGIN SELECT RAISE(ABORT,'archived case is immutable'); END`,
		`CREATE TRIGGER IF NOT EXISTS prevent_archived_qualification_version_update BEFORE UPDATE ON instrument_qualification_versions WHEN (SELECT archived FROM cases WHERE case_id=OLD.case_id)=1 BEGIN SELECT RAISE(ABORT,'archived case is immutable'); END`,
		`CREATE TRIGGER IF NOT EXISTS prevent_archived_qualification_version_delete BEFORE DELETE ON instrument_qualification_versions WHEN (SELECT archived FROM cases WHERE case_id=OLD.case_id)=1 BEGIN SELECT RAISE(ABORT,'archived case is immutable'); END`,
		`CREATE TRIGGER IF NOT EXISTS prevent_archived_assessment_update BEFORE UPDATE ON assessment_runs WHEN (SELECT archived FROM cases WHERE case_id=OLD.case_id)=1 BEGIN SELECT RAISE(ABORT,'archived case is immutable'); END`,
		`CREATE TRIGGER IF NOT EXISTS prevent_archived_assessment_delete BEFORE DELETE ON assessment_runs WHEN (SELECT archived FROM cases WHERE case_id=OLD.case_id)=1 BEGIN SELECT RAISE(ABORT,'archived case is immutable'); END`,
		`CREATE TRIGGER IF NOT EXISTS prevent_archived_assessment_details_update BEFORE UPDATE ON assessment_run_details WHEN (SELECT archived FROM cases WHERE case_id=OLD.case_id)=1 BEGIN SELECT RAISE(ABORT,'archived case is immutable'); END`,
		`CREATE TRIGGER IF NOT EXISTS prevent_archived_assessment_details_delete BEFORE DELETE ON assessment_run_details WHEN (SELECT archived FROM cases WHERE case_id=OLD.case_id)=1 BEGIN SELECT RAISE(ABORT,'archived case is immutable'); END`,
		`CREATE TRIGGER IF NOT EXISTS prevent_archived_deviation_update BEFORE UPDATE ON deviations WHEN (SELECT archived FROM cases WHERE case_id=OLD.case_id)=1 BEGIN SELECT RAISE(ABORT,'archived case is immutable'); END`,
		`CREATE TRIGGER IF NOT EXISTS prevent_archived_deviation_delete BEFORE DELETE ON deviations WHEN (SELECT archived FROM cases WHERE case_id=OLD.case_id)=1 BEGIN SELECT RAISE(ABORT,'archived case is immutable'); END`,
		`CREATE TRIGGER IF NOT EXISTS prevent_archived_decision_update BEFORE UPDATE ON activation_decisions WHEN (SELECT archived FROM cases WHERE case_id=OLD.case_id)=1 BEGIN SELECT RAISE(ABORT,'archived case is immutable'); END`,
		`CREATE TRIGGER IF NOT EXISTS prevent_archived_decision_delete BEFORE DELETE ON activation_decisions WHEN (SELECT archived FROM cases WHERE case_id=OLD.case_id)=1 BEGIN SELECT RAISE(ABORT,'archived case is immutable'); END`,
		`CREATE TRIGGER IF NOT EXISTS prevent_archived_observation_update BEFORE UPDATE ON trial_observations WHEN (SELECT archived FROM cases WHERE case_id=OLD.case_id)=1 BEGIN SELECT RAISE(ABORT,'archived case is immutable'); END`,
		`CREATE TRIGGER IF NOT EXISTS prevent_archived_observation_delete BEFORE DELETE ON trial_observations WHEN (SELECT archived FROM cases WHERE case_id=OLD.case_id)=1 BEGIN SELECT RAISE(ABORT,'archived case is immutable'); END`,
		`CREATE INDEX IF NOT EXISTS idx_cases_station ON cases(station_id)`,
		`CREATE INDEX IF NOT EXISTS idx_evidence_case ON measurement_evidence(case_id,observed_at)`,
		`CREATE INDEX IF NOT EXISTS idx_qualification_case ON instrument_qualifications(case_id,instrument_kind)`,
		`CREATE INDEX IF NOT EXISTS idx_deviation_case ON deviations(case_id,state)`,
	}
	for _, stmt := range stmts {
		if _, err := s.db.ExecContext(ctx, stmt); err != nil {
			return fmt.Errorf("数据库迁移失败: %w", err)
		}
	}
	return nil
}

func (t *Tx) RegisterTrial(ctx context.Context, c *domain.Case, d domain.Decision) error {
	if d.EffectiveUntil == nil {
		return fmt.Errorf("%w: 试用决定缺少结束时间", domain.ErrInvalid)
	}
	_, err := t.tx.ExecContext(ctx, `INSERT INTO active_trials(station_id,case_id,curve_version,effective_from,effective_until) VALUES(?,?,?,?,?)`, c.StationID, c.CaseID, d.CurveVersion, d.EffectiveFrom.Format(time.RFC3339Nano), d.EffectiveUntil.Format(time.RFC3339Nano))
	if err != nil {
		return fmt.Errorf("%w: 同一测站已有有效试用版本", domain.ErrConflict)
	}
	return nil
}

func (t *Tx) ReleaseTrial(ctx context.Context, stationID, caseID string) error {
	_, err := t.tx.ExecContext(ctx, `DELETE FROM active_trials WHERE station_id=? AND case_id=?`, stationID, caseID)
	return err
}

func (s *Store) Within(ctx context.Context, fn func(*Tx) error) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	wrapped := &Tx{tx: tx}
	if err = fn(wrapped); err != nil {
		_ = tx.Rollback()
		return err
	}
	return tx.Commit()
}

func (t *Tx) LoadCase(ctx context.Context, id string) (*domain.Case, error) {
	var body []byte
	if err := t.tx.QueryRowContext(ctx, `SELECT body FROM cases WHERE case_id=?`, id).Scan(&body); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, domain.ErrNotFound
		}
		return nil, err
	}
	var c domain.Case
	if err := json.Unmarshal(body, &c); err != nil {
		return nil, err
	}
	return &c, nil
}
func (s *Store) LoadCase(ctx context.Context, id string) (*domain.Case, error) {
	var body []byte
	if err := s.db.QueryRowContext(ctx, `SELECT body FROM cases WHERE case_id=?`, id).Scan(&body); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, domain.ErrNotFound
		}
		return nil, err
	}
	var c domain.Case
	if err := json.Unmarshal(body, &c); err != nil {
		return nil, err
	}
	return &c, nil
}

func (s *Store) CaseStation(ctx context.Context, id string) (string, error) {
	var station string
	if err := s.db.QueryRowContext(ctx, `SELECT station_id FROM cases WHERE case_id=?`, id).Scan(&station); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return "", domain.ErrNotFound
		}
		return "", err
	}
	return station, nil
}

func (t *Tx) InsertCase(ctx context.Context, c *domain.Case) error {
	b, _ := json.Marshal(c)
	_, err := t.tx.ExecContext(ctx, `INSERT INTO cases(case_id,station_id,revision,state,body,archived) VALUES(?,?,?,?,?,0)`, c.CaseID, c.StationID, c.Revision, c.State, b)
	if err != nil {
		return fmt.Errorf("%w: 案件标识已存在", domain.ErrConflict)
	}
	return t.syncNormalized(ctx, c)
}
func (t *Tx) SaveCase(ctx context.Context, c *domain.Case, expected int64) error {
	b, _ := json.Marshal(c)
	if err := t.syncNormalized(ctx, c); err != nil {
		return err
	}
	r, err := t.tx.ExecContext(ctx, `UPDATE cases SET revision=?,state=?,body=?,archived=? WHERE case_id=? AND revision=?`, c.Revision, c.State, b, boolInt(c.State == domain.Archived), c.CaseID, expected)
	if err != nil {
		return err
	}
	n, _ := r.RowsAffected()
	if n != 1 {
		return domain.ErrConflict
	}
	return nil
}

func (t *Tx) syncNormalized(ctx context.Context, c *domain.Case) error {
	for _, table := range []string{"measurement_evidence", "evidence_instrument_bindings", "instrument_qualifications", "instrument_qualification_versions", "assessment_runs", "assessment_run_details", "deviations", "activation_decisions", "trial_observations"} {
		if _, err := t.tx.ExecContext(ctx, `DELETE FROM `+table+` WHERE case_id=?`, c.CaseID); err != nil {
			return err
		}
	}
	for _, e := range c.Evidence {
		if _, err := t.tx.ExecContext(ctx, `INSERT INTO measurement_evidence(evidence_id,case_id,evidence_type,observed_at,water_level_m,discharge_m3s,source_ref,content_digest,quality_decision,decision_reason)VALUES(?,?,?,?,?,?,?,?,?,?)`, e.EvidenceID, c.CaseID, e.EvidenceType, e.ObservedAt.Format(time.RFC3339Nano), e.WaterLevelM, e.DischargeM3S, e.SourceRef, e.ContentDigest, e.QualityDecision, e.DecisionReason); err != nil {
			return err
		}
		for _, b := range e.InstrumentBindings {
			if _, err := t.tx.ExecContext(ctx, `INSERT INTO evidence_instrument_bindings(case_id,evidence_id,instrument_kind,instrument_id,qualification_id,certificate_digest)VALUES(?,?,?,?,?,?)`, c.CaseID, e.EvidenceID, b.InstrumentKind, b.InstrumentID, b.QualificationID, b.CertificateDigest); err != nil {
				return err
			}
		}
	}
	for _, q := range c.Qualifications {
		if _, err := t.tx.ExecContext(ctx, `INSERT INTO instrument_qualifications(qualification_id,case_id,instrument_id,instrument_kind,certificate_ref,calibrated_at,valid_until,usage_started_at,usage_ended_at,verdict)VALUES(?,?,?,?,?,?,?,?,?,?)`, q.QualificationID, c.CaseID, q.InstrumentID, q.InstrumentKind, q.CertificateRef, q.CalibratedAt.Format(time.RFC3339Nano), q.ValidUntil.Format(time.RFC3339Nano), q.UsageStartedAt.Format(time.RFC3339Nano), q.UsageEndedAt.Format(time.RFC3339Nano), q.Verdict); err != nil {
			return err
		}
	}
	for _, v := range c.QualificationVersions {
		body, _ := json.Marshal(v)
		if _, err := t.tx.ExecContext(ctx, `INSERT INTO instrument_qualification_versions(case_id,qualification_id,version,digest,previous_digest,body)VALUES(?,?,?,?,?,?)`, c.CaseID, v.QualificationID, v.Version, v.Digest, v.PreviousDigest, body); err != nil {
			return err
		}
	}
	if c.Assessment != nil {
		parameters, _ := json.Marshal(c.Assessment.Parameters)
		metrics, _ := json.Marshal(c.Assessment.ResidualMetrics)
		if _, err := t.tx.ExecContext(ctx, `INSERT INTO assessment_runs(run_id,case_id,input_digest,method_version,parameters_json,residual_metrics_json,lower_bound_m,upper_bound_m,extrapolation_ratio,verdict,completed_at)VALUES(?,?,?,?,?,?,?,?,?,?,?)`, c.Assessment.RunID, c.CaseID, c.Assessment.InputDigest, c.Assessment.MethodVersion, parameters, metrics, c.Assessment.LowerBoundM, c.Assessment.UpperBoundM, c.Assessment.ExtrapolationRatio, c.Assessment.Verdict, c.Assessment.CompletedAt.Format(time.RFC3339Nano)); err != nil {
			return err
		}
		body, _ := json.Marshal(c.Assessment)
		if _, err := t.tx.ExecContext(ctx, `INSERT INTO assessment_run_details(run_id,case_id,body)VALUES(?,?,?)`, c.Assessment.RunID, c.CaseID, body); err != nil {
			return err
		}
	}
	for _, d := range c.Deviations {
		body, _ := json.Marshal(d)
		if _, err := t.tx.ExecContext(ctx, `INSERT INTO deviations(deviation_id,case_id,source_gate,severity,state,body)VALUES(?,?,?,?,?,?)`, d.DeviationID, c.CaseID, d.SourceGate, d.Severity, d.State, body); err != nil {
			return err
		}
	}
	for _, d := range c.Decisions {
		var until any
		if d.EffectiveUntil != nil {
			until = d.EffectiveUntil.Format(time.RFC3339Nano)
		}
		if _, err := t.tx.ExecContext(ctx, `INSERT INTO activation_decisions(decision_id,case_id,decision_type,curve_version,authorized_by,effective_from,effective_until,lower_bound_m,upper_bound_m,rollback_condition,replaces_version)VALUES(?,?,?,?,?,?,?,?,?,?,?)`, d.DecisionID, c.CaseID, d.DecisionType, d.CurveVersion, d.AuthorizedBy, d.EffectiveFrom.Format(time.RFC3339Nano), until, d.LowerBoundM, d.UpperBoundM, d.RollbackCondition, d.ReplacesVersion); err != nil {
			return err
		}
	}
	for _, o := range c.TrialObservations {
		if _, err := t.tx.ExecContext(ctx, `INSERT INTO trial_observations(observation_id,case_id,observed_at,water_level_m,measured_discharge_m3s,predicted_discharge_m3s,relative_bias,verdict)VALUES(?,?,?,?,?,?,?,?)`, o.ObservationID, c.CaseID, o.ObservedAt.Format(time.RFC3339Nano), o.WaterLevelM, o.MeasuredDischargeM3S, o.PredictedDischargeM3S, o.RelativeBias, o.Verdict); err != nil {
			return err
		}
	}
	return nil
}
func boolInt(v bool) int {
	if v {
		return 1
	}
	return 0
}

func (t *Tx) GetIdempotent(ctx context.Context, request string) (string, string, int, []byte, bool, error) {
	var caseID, command string
	var status int
	var body []byte
	err := t.tx.QueryRowContext(ctx, `SELECT i.case_id,COALESCE(c.command_type,''),i.status,i.response FROM idempotency i LEFT JOIN idempotency_commands c ON c.request_id=i.request_id WHERE i.request_id=?`, request).Scan(&caseID, &command, &status, &body)
	if errors.Is(err, sql.ErrNoRows) {
		return "", "", 0, nil, false, nil
	}
	return caseID, command, status, body, err == nil, err
}
func (t *Tx) SaveIdempotent(ctx context.Context, request, caseID, command string, status int, body []byte) error {
	_, err := t.tx.ExecContext(ctx, `INSERT INTO idempotency(request_id,case_id,status,response,created_at)VALUES(?,?,?,?,?)`, request, caseID, status, body, time.Now().UTC().Format(time.RFC3339Nano))
	if err != nil {
		return err
	}
	_, err = t.tx.ExecContext(ctx, `INSERT INTO idempotency_commands(request_id,command_type)VALUES(?,?)`, request, command)
	return err
}

func (t *Tx) AppendEvent(ctx context.Context, e domain.AuditEvent) error {
	b, _ := json.Marshal(e)
	_, err := t.tx.ExecContext(ctx, `INSERT INTO audit_events(case_id,sequence_no,event_digest,body) VALUES(?,?,?,?)`, e.CaseID, e.SequenceNo, e.EventDigest, b)
	return err
}
func (t *Tx) Events(ctx context.Context, caseID string) ([]domain.AuditEvent, error) {
	rows, err := t.tx.QueryContext(ctx, `SELECT body FROM audit_events WHERE case_id=? ORDER BY sequence_no`, caseID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []domain.AuditEvent
	for rows.Next() {
		var b []byte
		var e domain.AuditEvent
		if err = rows.Scan(&b); err != nil {
			return nil, err
		}
		if err = json.Unmarshal(b, &e); err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	return out, rows.Err()
}
func (s *Store) Events(ctx context.Context, caseID string) ([]domain.AuditEvent, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT body FROM audit_events WHERE case_id=? ORDER BY sequence_no`, caseID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []domain.AuditEvent
	for rows.Next() {
		var b []byte
		var e domain.AuditEvent
		if err = rows.Scan(&b); err != nil {
			return nil, err
		}
		if err = json.Unmarshal(b, &e); err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	return out, rows.Err()
}

func (s *Store) IdempotentStatus(ctx context.Context, caseID, requestID string) (int, []byte, error) {
	var status int
	var response []byte
	err := s.db.QueryRowContext(ctx, `SELECT status,response FROM idempotency WHERE case_id=? AND request_id=?`, caseID, requestID).Scan(&status, &response)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, nil, domain.ErrNotFound
	}
	return status, response, err
}

func (t *Tx) Activate(ctx context.Context, c *domain.Case, d domain.Decision, expectedDigest string) error {
	var previous string
	var previousCase, previousFrom, previousReplaces string
	err := t.tx.QueryRowContext(ctx, `SELECT case_id,curve_version,effective_from,replaces_version FROM station_curves WHERE station_id=?`, c.StationID).Scan(&previousCase, &previous, &previousFrom, &previousReplaces)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	current := map[string]string{}
	if err == nil {
		current = map[string]string{"case_id": previousCase, "curve_version": previous, "effective_from": previousFrom, "replaces_version": previousReplaces}
	}
	if domain.Digest(current) != expectedDigest {
		return fmt.Errorf("%w: current_version_digest 已变化", domain.ErrConflict)
	}
	if err == nil {
		var from time.Time
		from, _ = time.Parse(time.RFC3339Nano, previousFrom)
		if !d.EffectiveFrom.After(from) {
			return fmt.Errorf("%w: effective_from 必须晚于当前版本", domain.ErrInvalid)
		}
		if _, err = t.tx.ExecContext(ctx, `UPDATE station_curve_history SET effective_until=? WHERE station_id=? AND curve_version=? AND effective_until IS NULL`, d.EffectiveFrom.UTC().Format(time.RFC3339Nano), c.StationID, previous); err != nil {
			return err
		}
	}
	d.ReplacesVersion = previous
	c.Decisions[len(c.Decisions)-1].ReplacesVersion = previous
	_, err = t.tx.ExecContext(ctx, `INSERT INTO station_curves(station_id,case_id,curve_version,effective_from,replaces_version)VALUES(?,?,?,?,?) ON CONFLICT(station_id) DO UPDATE SET case_id=excluded.case_id,curve_version=excluded.curve_version,effective_from=excluded.effective_from,replaces_version=excluded.replaces_version`, c.StationID, c.CaseID, d.CurveVersion, d.EffectiveFrom.UTC().Format(time.RFC3339Nano), previous)
	if err != nil {
		return err
	}
	if _, err = t.tx.ExecContext(ctx, `INSERT INTO station_curve_history(station_id,case_id,curve_version,effective_from,effective_until,decision_id,replaces_version)VALUES(?,?,?,?,NULL,?,?)`, c.StationID, c.CaseID, d.CurveVersion, d.EffectiveFrom.UTC().Format(time.RFC3339Nano), d.DecisionID, previous); err != nil {
		return fmt.Errorf("%w: 测站版本生效区间冲突", domain.ErrConflict)
	}
	_, err = t.tx.ExecContext(ctx, `DELETE FROM active_trials WHERE station_id=? AND case_id=?`, c.StationID, c.CaseID)
	return err
}
func (s *Store) CurrentCurve(ctx context.Context, station string) (map[string]string, error) {
	v, err := s.CurveAsOf(ctx, station, time.Now().UTC())
	if err != nil {
		return nil, err
	}
	out := map[string]string{}
	for _, key := range []string{"case_id", "curve_version", "effective_from", "replaces_version"} {
		if x, ok := v[key].(string); ok {
			out[key] = x
		}
	}
	return out, nil
}

func (s *Store) CurvePointer(ctx context.Context, station string) (map[string]string, error) {
	var caseID, version, effective, replaces string
	err := s.db.QueryRowContext(ctx, `SELECT case_id,curve_version,effective_from,replaces_version FROM station_curves WHERE station_id=?`, station).Scan(&caseID, &version, &effective, &replaces)
	if errors.Is(err, sql.ErrNoRows) {
		return map[string]string{}, nil
	}
	if err != nil {
		return nil, err
	}
	return map[string]string{"case_id": caseID, "curve_version": version, "effective_from": effective, "replaces_version": replaces}, nil
}
func (s *Store) CurveHistory(ctx context.Context, station string) ([]map[string]any, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT case_id,curve_version,effective_from,effective_until,decision_id,replaces_version FROM station_curve_history WHERE station_id=? ORDER BY effective_from,curve_version`, station)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []map[string]any{}
	for rows.Next() {
		var caseID, version, from, decision, replaces string
		var until sql.NullString
		if err = rows.Scan(&caseID, &version, &from, &until, &decision, &replaces); err != nil {
			return nil, err
		}
		m := map[string]any{"case_id": caseID, "curve_version": version, "effective_from": from, "decision_id": decision, "replaces_version": replaces}
		if until.Valid {
			m["effective_until"] = until.String
		}
		out = append(out, m)
	}
	return out, rows.Err()
}
func (s *Store) CurveAsOf(ctx context.Context, station string, at time.Time) (map[string]any, error) {
	var caseID, version, from, decision, replaces string
	var until sql.NullString
	err := s.db.QueryRowContext(ctx, `SELECT case_id,curve_version,effective_from,effective_until,decision_id,replaces_version FROM station_curve_history WHERE station_id=? AND effective_from<=? AND (effective_until IS NULL OR effective_until>?) ORDER BY effective_from DESC LIMIT 1`, station, at.UTC().Format(time.RFC3339Nano), at.UTC().Format(time.RFC3339Nano)).Scan(&caseID, &version, &from, &until, &decision, &replaces)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, domain.ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	m := map[string]any{"case_id": caseID, "curve_version": version, "effective_from": from, "decision_id": decision, "replaces_version": replaces}
	if until.Valid {
		m["effective_until"] = until.String
	}
	return m, nil
}
func (t *Tx) SaveArchive(ctx context.Context, a audit.Archive) error {
	b, _ := json.Marshal(a)
	_, err := t.tx.ExecContext(ctx, `INSERT INTO archives(case_id,digest,body,created_at)VALUES(?,?,?,?)`, a.Case.CaseID, a.Digest, b, a.CreatedAt.Format(time.RFC3339Nano))
	return err
}
func (s *Store) Archive(ctx context.Context, id string) (audit.Archive, error) {
	var b []byte
	err := s.db.QueryRowContext(ctx, `SELECT body FROM archives WHERE case_id=?`, id).Scan(&b)
	if errors.Is(err, sql.ErrNoRows) {
		return audit.Archive{}, domain.ErrNotFound
	}
	if err != nil {
		return audit.Archive{}, err
	}
	var a audit.Archive
	err = json.Unmarshal(b, &a)
	return a, err
}
