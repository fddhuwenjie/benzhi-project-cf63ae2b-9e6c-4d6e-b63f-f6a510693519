package api

import (
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"time"

	"benzhi-project-cf63ae2b-9e6c-4d6e-b63f-f6a510693519/internal/application"
	"benzhi-project-cf63ae2b-9e6c-4d6e-b63f-f6a510693519/internal/domain"
)

const maxRequestBytes = 1 << 20

type Server struct {
	service *application.Service
	log     *slog.Logger
}

func New(service *application.Service, logger *slog.Logger) http.Handler {
	if logger == nil {
		logger = slog.Default()
	}
	s := &Server{service: service, log: logger}
	mux := http.NewServeMux()
	s.RegisterRoutes(mux)
	return s.middleware(mux)
}

func (s *Server) RegisterRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /healthz", s.HealthHandler)
	mux.HandleFunc("POST /api/v1/curve-cases", s.CreateCaseHandler)
	mux.HandleFunc("GET /api/v1/curve-cases", s.ListCasesHandler)
	mux.HandleFunc("GET /api/v1/curve-cases/{case_id}", s.GetCaseHandler)
	mux.HandleFunc("POST /api/v1/curve-cases/{case_id}/evidence", s.AddEvidenceHandler)
	mux.HandleFunc("POST /api/v1/curve-cases/{case_id}/evidence/batches", s.AddEvidenceBatchHandler)
	mux.HandleFunc("POST /api/v1/curve-cases/{case_id}/evidence/{evidence_id}/corrections", s.CorrectEvidenceHandler)
	mux.HandleFunc("GET /api/v1/curve-cases/{case_id}/evidence/{evidence_id}/versions", s.EvidenceVersionsHandler)
	mux.HandleFunc("GET /api/v1/curve-cases/{case_id}/evidence/quality-preflight", s.QualityPreflightHandler)
	mux.HandleFunc("POST /api/v1/curve-cases/{case_id}/evidence/quality-rejudgments", s.BulkQualityHandler)
	mux.HandleFunc("POST /api/v1/curve-cases/{case_id}/instrument-qualifications", s.AddQualificationHandler)
	mux.HandleFunc("GET /api/v1/curve-cases/{case_id}/instrument-qualifications/coverage-matrix", s.CoverageMatrixHandler)
	mux.HandleFunc("POST /api/v1/curve-cases/{case_id}/instrument-qualifications/{qualification_id}/corrections", s.CorrectQualificationHandler)
	mux.HandleFunc("POST /api/v1/curve-cases/{case_id}/instrument-qualifications/{qualification_id}/invalidations", s.InstrumentInvalidationHandler)
	mux.HandleFunc("GET /api/v1/curve-cases/{case_id}/instrument-qualifications/{qualification_id}/versions", s.QualificationVersionsHandler)
	mux.HandleFunc("POST /api/v1/curve-cases/{case_id}/freeze-baseline", s.FreezeBaselineHandler)
	mux.HandleFunc("GET /api/v1/curve-cases/{case_id}/baseline-preflight", s.BaselinePreflightHandler)
	mux.HandleFunc("POST /api/v1/curve-cases/{case_id}/qualify-evidence", s.QualifyEvidenceHandler)
	mux.HandleFunc("POST /api/v1/curve-cases/{case_id}/assessments", s.AssessmentHandler)
	mux.HandleFunc("GET /api/v1/curve-cases/{case_id}/assessments/diagnostics", s.DiagnosticsHandler)
	mux.HandleFunc("GET /api/v1/curve-cases/{case_id}/assessments/{run_id}/replay-verification", s.ReplayAssessmentHandler)
	mux.HandleFunc("POST /api/v1/curve-cases/{case_id}/deviations", s.AddDeviationHandler)
	mux.HandleFunc("POST /api/v1/curve-cases/{case_id}/deviations/{deviation_id}/remediate", s.RemediateDeviationHandler)
	mux.HandleFunc("POST /api/v1/curve-cases/{case_id}/deviations/{deviation_id}/containment", s.ContainDeviationHandler)
	mux.HandleFunc("POST /api/v1/curve-cases/{case_id}/deviations/{deviation_id}/root-cause", s.AnalyzeDeviationHandler)
	mux.HandleFunc("POST /api/v1/curve-cases/{case_id}/deviations/{deviation_id}/correction", s.CorrectDeviationHandler)
	mux.HandleFunc("POST /api/v1/curve-cases/{case_id}/deviations/{deviation_id}/verification", s.VerifyDeviationHandler)
	mux.HandleFunc("GET /api/v1/curve-cases/{case_id}/deviations", s.DeviationsHandler)
	mux.HandleFunc("GET /api/v1/curve-cases/{case_id}/deviations/action-queue", s.DeviationActionQueueHandler)
	mux.HandleFunc("POST /api/v1/curve-cases/{case_id}/deviations/{deviation_id}/due-date-revisions", s.DueDateRevisionHandler)
	mux.HandleFunc("POST /api/v1/curve-cases/{case_id}/close-deviations", s.CloseDeviationsHandler)
	mux.HandleFunc("POST /api/v1/curve-cases/{case_id}/reviews", s.ReviewHandler)
	mux.HandleFunc("POST /api/v1/curve-cases/{case_id}/reviews/issues/{issue_id}/responses", s.ReviewIssueResponseHandler)
	mux.HandleFunc("POST /api/v1/curve-cases/{case_id}/reviews/resubmit", s.ReviewResubmitHandler)
	mux.HandleFunc("GET /api/v1/curve-cases/{case_id}/reviews/history", s.ReviewHistoryHandler)
	mux.HandleFunc("GET /api/v1/curve-cases/{case_id}/reviews/preflight", s.ReviewPreflightHandler)
	mux.HandleFunc("POST /api/v1/curve-cases/{case_id}/trial-decisions", s.TrialDecisionHandler)
	mux.HandleFunc("POST /api/v1/curve-cases/{case_id}/trial-expiry-settlements", s.TrialExpirySettlementHandler)
	mux.HandleFunc("POST /api/v1/curve-cases/{case_id}/trial-observations", s.TrialObservationHandler)
	mux.HandleFunc("POST /api/v1/curve-cases/{case_id}/trial-observations/{observation_id}/replacements", s.TrialReplacementHandler)
	mux.HandleFunc("GET /api/v1/curve-cases/{case_id}/trial-suspensions", s.TrialSuspensionsHandler)
	mux.HandleFunc("GET /api/v1/curve-cases/{case_id}/trial-progress", s.TrialProgressHandler)
	mux.HandleFunc("POST /api/v1/curve-cases/{case_id}/trial-suspensions/investigation", s.InvestigationHandler)
	mux.HandleFunc("POST /api/v1/curve-cases/{case_id}/trial-suspensions/recovery", s.RecoveryHandler)
	mux.HandleFunc("GET /api/v1/curve-cases/{case_id}/activation-preflight", s.ActivationPreflightHandler)
	mux.HandleFunc("POST /api/v1/curve-cases/{case_id}/activation-decisions", s.ActivationHandler)
	mux.HandleFunc("POST /api/v1/curve-cases/{case_id}/archive", s.ArchiveHandler)
	mux.HandleFunc("GET /api/v1/curve-cases/{case_id}/timeline", s.TimelineHandler)
	mux.HandleFunc("GET /api/v1/curve-cases/{case_id}/archive", s.GetArchiveHandler)
	mux.HandleFunc("GET /api/v1/curve-cases/{case_id}/archive/verification", s.ArchiveVerificationHandler)
	mux.HandleFunc("GET /api/v1/stations/{station_id}/current-curve", s.CurrentCurveHandler)
	mux.HandleFunc("GET /api/v1/stations/{station_id}/curve-history", s.CurveHistoryHandler)
	mux.HandleFunc("GET /api/v1/stations/{station_id}/curve-as-of", s.CurveAsOfHandler)
	mux.HandleFunc("GET /api/v1/stations/{station_id}/certification-continuity", s.CertificationContinuityHandler)
}

func (s *Server) middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		w.Header().Set("X-Content-Type-Options", "nosniff")
		correlation := strings.TrimSpace(r.Header.Get("X-Correlation-ID"))
		if len(correlation) > 120 {
			writeError(w, http.StatusBadRequest, "invalid_correlation_id", "关联标识长度超限")
			return
		}
		if correlation != "" {
			w.Header().Set("X-Correlation-ID", correlation)
		}
		next.ServeHTTP(w, r)
		s.log.Info("http_request", "method", r.Method, "path", r.URL.Path, "duration_ms", time.Since(start).Milliseconds())
	})
}

func decode(w http.ResponseWriter, r *http.Request, dst any) error {
	if ct := r.Header.Get("Content-Type"); ct != "" && !strings.HasPrefix(strings.ToLower(ct), "application/json") {
		return errors.New("Content-Type 必须为 application/json")
	}
	r.Body = http.MaxBytesReader(w, r.Body, maxRequestBytes)
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	if err := dec.Decode(dst); err != nil {
		return err
	}
	var extra any
	if err := dec.Decode(&extra); err != io.EOF {
		return errors.New("请求体只能包含一个 JSON 对象")
	}
	return nil
}
func writeJSON(w http.ResponseWriter, status int, v any) {
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}
func writeResult(w http.ResponseWriter, result application.Result) {
	if result.Replayed {
		w.Header().Set("Idempotent-Replayed", "true")
	}
	w.WriteHeader(result.Status)
	_, _ = w.Write(result.Body)
	_, _ = w.Write([]byte("\n"))
}
func writeError(w http.ResponseWriter, status int, code, message string) {
	writeJSON(w, status, map[string]any{"error": map[string]any{"code": code, "message": message, "retryable": status == http.StatusConflict}})
}
func handleErr(w http.ResponseWriter, err error) {
	var duplicate *domain.CandidateConflictError
	if errors.As(err, &duplicate) {
		writeJSON(w, http.StatusConflict, map[string]any{"error": map[string]any{"code": "duplicate_candidate", "message": err.Error(), "retryable": false, "existing_case": duplicate}})
		return
	}
	var structured *domain.StructuredError
	if errors.As(err, &structured) {
		kind := application.ErrorKind(err)
		status := http.StatusBadRequest
		if kind == "gate_failed" {
			status = http.StatusUnprocessableEntity
		}
		writeJSON(w, status, map[string]any{"error": map[string]any{"code": kind, "message": err.Error(), "retryable": false, "issues": structured.Issues}})
		return
	}
	var baselineConflict *application.BaselineConflictError
	if errors.As(err, &baselineConflict) {
		writeJSON(w, http.StatusConflict, map[string]any{"error": map[string]any{"code": "revision_conflict", "message": err.Error(), "retryable": true, "latest_preflight": baselineConflict.Latest}})
		return
	}
	kind := application.ErrorKind(err)
	status := http.StatusUnprocessableEntity
	switch kind {
	case "not_found":
		status = http.StatusNotFound
	case "revision_conflict":
		status = http.StatusConflict
	case "case_archived":
		status = http.StatusConflict
	case "validation_failed":
		status = http.StatusBadRequest
	case "internal_error":
		status = http.StatusInternalServerError
	}
	message := err.Error()
	if status == http.StatusInternalServerError {
		message = "服务内部错误"
	}
	writeError(w, status, kind, message)
}
func run[T any](s *Server, w http.ResponseWriter, r *http.Request, fn func(T) (application.Result, error)) {
	var in T
	if err := decode(w, r, &in); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_json", err.Error())
		return
	}
	result, err := fn(in)
	if err != nil {
		handleErr(w, err)
		return
	}
	writeResult(w, result)
}

func (s *Server) HealthHandler(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	if err := s.service.Store().Ping(ctx); err != nil {
		writeError(w, http.StatusServiceUnavailable, "database_unavailable", "数据库不可用")
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}
func (s *Server) CreateCaseHandler(w http.ResponseWriter, r *http.Request) {
	run(s, w, r, func(in application.CreateInput) (application.Result, error) { return s.service.Create(r.Context(), in) })
}
func (s *Server) GetCaseHandler(w http.ResponseWriter, r *http.Request) {
	c, err := s.service.Get(r.Context(), r.PathValue("case_id"))
	if err != nil {
		handleErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, c)
}
func (s *Server) AddEvidenceHandler(w http.ResponseWriter, r *http.Request) {
	run(s, w, r, func(in application.EvidenceInput) (application.Result, error) {
		return s.service.AddEvidence(r.Context(), r.PathValue("case_id"), in)
	})
}
func (s *Server) AddQualificationHandler(w http.ResponseWriter, r *http.Request) {
	run(s, w, r, func(in application.QualificationInput) (application.Result, error) {
		return s.service.AddQualification(r.Context(), r.PathValue("case_id"), in)
	})
}
func (s *Server) FreezeBaselineHandler(w http.ResponseWriter, r *http.Request) {
	run(s, w, r, func(in application.FreezeInput) (application.Result, error) {
		return s.service.Freeze(r.Context(), r.PathValue("case_id"), in)
	})
}
func (s *Server) QualifyEvidenceHandler(w http.ResponseWriter, r *http.Request) {
	run(s, w, r, func(in application.Meta) (application.Result, error) {
		return s.service.Qualify(r.Context(), r.PathValue("case_id"), in)
	})
}
func (s *Server) AssessmentHandler(w http.ResponseWriter, r *http.Request) {
	run(s, w, r, func(in application.AssessmentInput) (application.Result, error) {
		return s.service.Assess(r.Context(), r.PathValue("case_id"), in)
	})
}
func (s *Server) AddDeviationHandler(w http.ResponseWriter, r *http.Request) {
	run(s, w, r, func(in application.DeviationInput) (application.Result, error) {
		return s.service.AddDeviation(r.Context(), r.PathValue("case_id"), in)
	})
}
func (s *Server) RemediateDeviationHandler(w http.ResponseWriter, r *http.Request) {
	run(s, w, r, func(in application.RemediationInput) (application.Result, error) {
		return s.service.Remediate(r.Context(), r.PathValue("case_id"), r.PathValue("deviation_id"), in)
	})
}
func (s *Server) CloseDeviationsHandler(w http.ResponseWriter, r *http.Request) {
	run(s, w, r, func(in application.Meta) (application.Result, error) {
		return s.service.CloseNoDeviations(r.Context(), r.PathValue("case_id"), in)
	})
}
func (s *Server) ReviewHandler(w http.ResponseWriter, r *http.Request) {
	run(s, w, r, func(in application.ReviewInput) (application.Result, error) {
		return s.service.Review(r.Context(), r.PathValue("case_id"), in)
	})
}
func (s *Server) TrialDecisionHandler(w http.ResponseWriter, r *http.Request) {
	run(s, w, r, func(in application.TrialInput) (application.Result, error) {
		return s.service.IssueTrial(r.Context(), r.PathValue("case_id"), in)
	})
}
func (s *Server) InstrumentInvalidationHandler(w http.ResponseWriter, r *http.Request) {
	run(s, w, r, func(in application.InstrumentInvalidationInput) (application.Result, error) {
		return s.service.InvalidateInstrument(r.Context(), r.PathValue("case_id"), r.PathValue("qualification_id"), in)
	})
}
func (s *Server) TrialExpirySettlementHandler(w http.ResponseWriter, r *http.Request) {
	run(s, w, r, func(in application.Meta) (application.Result, error) {
		return s.service.SettleTrialExpiry(r.Context(), r.PathValue("case_id"), in)
	})
}
func (s *Server) TrialObservationHandler(w http.ResponseWriter, r *http.Request) {
	run(s, w, r, func(in application.ObservationInput) (application.Result, error) {
		return s.service.ObserveTrial(r.Context(), r.PathValue("case_id"), in)
	})
}
func (s *Server) ActivationHandler(w http.ResponseWriter, r *http.Request) {
	run(s, w, r, func(in application.ActivationInput) (application.Result, error) {
		return s.service.Activate(r.Context(), r.PathValue("case_id"), in)
	})
}
func (s *Server) ArchiveHandler(w http.ResponseWriter, r *http.Request) {
	run(s, w, r, func(in application.Meta) (application.Result, error) {
		return s.service.Archive(r.Context(), r.PathValue("case_id"), in)
	})
}
func (s *Server) TimelineHandler(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	in := application.TimelineInput{EventType: q.Get("event_type"), ActorID: q.Get("actor_id"), RequestID: q.Get("request_id"), Cursor: q.Get("cursor"), Limit: 50}
	parse := func(name string) (int64, bool) {
		raw := q.Get(name)
		if raw == "" {
			return 0, true
		}
		v, err := strconv.ParseInt(raw, 10, 64)
		return v, err == nil
	}
	var ok bool
	in.StartSequence, ok = parse("start_sequence_no")
	if !ok {
		writeError(w, 400, "validation_failed", "start_sequence_no 必须为整数")
		return
	}
	in.EndSequence, ok = parse("end_sequence_no")
	if !ok {
		writeError(w, 400, "validation_failed", "end_sequence_no 必须为整数")
		return
	}
	if raw := q.Get("limit"); raw != "" {
		v, err := strconv.Atoi(raw)
		if err != nil {
			writeError(w, 400, "validation_failed", "limit 必须为整数")
			return
		}
		in.Limit = v
	}
	page, err := s.service.TimelinePage(r.Context(), r.PathValue("case_id"), in)
	if err != nil {
		handleErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, page)
}
func (s *Server) GetArchiveHandler(w http.ResponseWriter, r *http.Request) {
	a, err := s.service.GetArchive(r.Context(), r.PathValue("case_id"))
	if err != nil {
		handleErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, a)
}
func (s *Server) CurrentCurveHandler(w http.ResponseWriter, r *http.Request) {
	v, err := s.service.CurrentCurve(r.Context(), r.PathValue("station_id"))
	if err != nil {
		handleErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, v)
}

func (s *Server) CorrectEvidenceHandler(w http.ResponseWriter, r *http.Request) {
	run(s, w, r, func(in application.EvidenceCorrectionInput) (application.Result, error) {
		return s.service.CorrectEvidence(r.Context(), r.PathValue("case_id"), r.PathValue("evidence_id"), in)
	})
}
func (s *Server) EvidenceVersionsHandler(w http.ResponseWriter, r *http.Request) {
	v, err := s.service.EvidenceVersions(r.Context(), r.PathValue("case_id"), r.PathValue("evidence_id"))
	if err != nil {
		handleErr(w, err)
		return
	}
	writeJSON(w, 200, v)
}
func (s *Server) QualityPreflightHandler(w http.ResponseWriter, r *http.Request) {
	v, err := s.service.QualityPreflight(r.Context(), r.PathValue("case_id"))
	if err != nil {
		handleErr(w, err)
		return
	}
	writeJSON(w, 200, v)
}
func (s *Server) BulkQualityHandler(w http.ResponseWriter, r *http.Request) {
	run(s, w, r, func(in application.BulkQualityInput) (application.Result, error) {
		return s.service.BulkQuality(r.Context(), r.PathValue("case_id"), in)
	})
}
func (s *Server) CoverageMatrixHandler(w http.ResponseWriter, r *http.Request) {
	v, err := s.service.CoverageMatrix(r.Context(), r.PathValue("case_id"))
	if err != nil {
		handleErr(w, err)
		return
	}
	writeJSON(w, 200, v)
}
func (s *Server) DiagnosticsHandler(w http.ResponseWriter, r *http.Request) {
	v, err := s.service.Diagnostics(r.Context(), r.PathValue("case_id"), r.URL.Query().Get("band"), r.URL.Query().Get("verdict"), r.URL.Query().Get("side"))
	if err != nil {
		handleErr(w, err)
		return
	}
	writeJSON(w, 200, v)
}
func (s *Server) ContainDeviationHandler(w http.ResponseWriter, r *http.Request) {
	run(s, w, r, func(in application.DeviationStepInput) (application.Result, error) {
		return s.service.ContainDeviation(r.Context(), r.PathValue("case_id"), r.PathValue("deviation_id"), in)
	})
}
func (s *Server) AnalyzeDeviationHandler(w http.ResponseWriter, r *http.Request) {
	run(s, w, r, func(in application.DeviationStepInput) (application.Result, error) {
		return s.service.AnalyzeDeviation(r.Context(), r.PathValue("case_id"), r.PathValue("deviation_id"), in)
	})
}
func (s *Server) CorrectDeviationHandler(w http.ResponseWriter, r *http.Request) {
	run(s, w, r, func(in application.DeviationStepInput) (application.Result, error) {
		return s.service.CorrectDeviation(r.Context(), r.PathValue("case_id"), r.PathValue("deviation_id"), in)
	})
}
func (s *Server) VerifyDeviationHandler(w http.ResponseWriter, r *http.Request) {
	run(s, w, r, func(in application.DeviationRetestInput) (application.Result, error) {
		return s.service.VerifyDeviation(r.Context(), r.PathValue("case_id"), r.PathValue("deviation_id"), in)
	})
}
func (s *Server) DeviationsHandler(w http.ResponseWriter, r *http.Request) {
	v, err := s.service.Deviations(r.Context(), r.PathValue("case_id"), r.URL.Query().Get("state"), r.URL.Query().Get("severity"))
	if err != nil {
		handleErr(w, err)
		return
	}
	writeJSON(w, 200, map[string]any{"deviations": v})
}
func (s *Server) ReviewIssueResponseHandler(w http.ResponseWriter, r *http.Request) {
	run(s, w, r, func(in application.ReviewIssueResponseInput) (application.Result, error) {
		return s.service.RespondReviewIssue(r.Context(), r.PathValue("case_id"), r.PathValue("issue_id"), in)
	})
}
func (s *Server) ReviewResubmitHandler(w http.ResponseWriter, r *http.Request) {
	run(s, w, r, func(in application.ReviewResubmitInput) (application.Result, error) {
		return s.service.ResubmitReview(r.Context(), r.PathValue("case_id"), in)
	})
}
func (s *Server) ReviewHistoryHandler(w http.ResponseWriter, r *http.Request) {
	v, err := s.service.ReviewHistory(r.Context(), r.PathValue("case_id"))
	if err != nil {
		handleErr(w, err)
		return
	}
	writeJSON(w, 200, map[string]any{"review_rounds": v})
}
func (s *Server) TrialSuspensionsHandler(w http.ResponseWriter, r *http.Request) {
	v, err := s.service.Suspensions(r.Context(), r.PathValue("case_id"))
	if err != nil {
		handleErr(w, err)
		return
	}
	writeJSON(w, 200, map[string]any{"suspensions": v})
}
func (s *Server) InvestigationHandler(w http.ResponseWriter, r *http.Request) {
	run(s, w, r, func(in application.InvestigationInput) (application.Result, error) {
		return s.service.SubmitInvestigation(r.Context(), r.PathValue("case_id"), in)
	})
}
func (s *Server) RecoveryHandler(w http.ResponseWriter, r *http.Request) {
	run(s, w, r, func(in application.RecoveryInput) (application.Result, error) {
		return s.service.RecoverTrial(r.Context(), r.PathValue("case_id"), in)
	})
}
func (s *Server) ActivationPreflightHandler(w http.ResponseWriter, r *http.Request) {
	at, err := time.Parse(time.RFC3339Nano, r.URL.Query().Get("effective_from"))
	if err != nil {
		writeError(w, 400, "validation_failed", "effective_from 必须为 RFC3339 时刻")
		return
	}
	v, err := s.service.ActivationPreflight(r.Context(), r.PathValue("case_id"), at)
	if err != nil {
		handleErr(w, err)
		return
	}
	writeJSON(w, 200, v)
}
func (s *Server) CurveHistoryHandler(w http.ResponseWriter, r *http.Request) {
	v, err := s.service.CurveHistory(r.Context(), r.PathValue("station_id"))
	if err != nil {
		handleErr(w, err)
		return
	}
	writeJSON(w, 200, map[string]any{"versions": v})
}
func (s *Server) CurveAsOfHandler(w http.ResponseWriter, r *http.Request) {
	at, err := time.Parse(time.RFC3339Nano, r.URL.Query().Get("as_of"))
	if err != nil {
		writeError(w, 400, "validation_failed", "as_of 必须为 RFC3339 时刻")
		return
	}
	v, err := s.service.CurveAsOf(r.Context(), r.PathValue("station_id"), at)
	if err != nil {
		handleErr(w, err)
		return
	}
	writeJSON(w, 200, v)
}
func (s *Server) CertificationContinuityHandler(w http.ResponseWriter, r *http.Request) {
	v, err := s.service.CertificationContinuity(r.Context(), r.PathValue("station_id"))
	if err != nil {
		handleErr(w, err)
		return
	}
	writeJSON(w, 200, v)
}
