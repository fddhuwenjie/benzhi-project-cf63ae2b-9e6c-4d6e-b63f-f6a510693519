package api

import (
	"net/http"
	"strconv"
	"strings"
	"time"

	"benzhi-project-cf63ae2b-9e6c-4d6e-b63f-f6a510693519/internal/application"
	"benzhi-project-cf63ae2b-9e6c-4d6e-b63f-f6a510693519/internal/domain"
)

func (s *Server) AddEvidenceBatchHandler(w http.ResponseWriter, r *http.Request) {
	run(s, w, r, func(in application.EvidenceBatchInput) (application.Result, error) {
		return s.service.AddEvidenceBatch(r.Context(), r.PathValue("case_id"), in)
	})
}
func (s *Server) BaselinePreflightHandler(w http.ResponseWriter, r *http.Request) {
	v, err := s.service.BaselinePreflight(r.Context(), r.PathValue("case_id"))
	if err != nil {
		handleErr(w, err)
		return
	}
	writeJSON(w, 200, v)
}
func (s *Server) ReplayAssessmentHandler(w http.ResponseWriter, r *http.Request) {
	v, err := s.service.ReplayAssessment(r.Context(), r.PathValue("case_id"), r.PathValue("run_id"))
	if err != nil {
		handleErr(w, err)
		return
	}
	writeJSON(w, 200, v)
}
func (s *Server) TrialReplacementHandler(w http.ResponseWriter, r *http.Request) {
	run(s, w, r, func(in application.ReplacementInput) (application.Result, error) {
		return s.service.ReplaceTrialObservation(r.Context(), r.PathValue("case_id"), r.PathValue("observation_id"), in)
	})
}

func (s *Server) ListCasesHandler(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	in := application.CaseListInput{StationID: q.Get("station_id"), OwnerID: q.Get("owner_id"), State: domain.State(q.Get("state")), Cursor: q.Get("cursor"), Limit: 50}
	if raw := q.Get("limit"); raw != "" {
		n, err := strconv.Atoi(raw)
		if err != nil || n < 1 {
			writeError(w, 400, "validation_failed", "limit 必须是正整数")
			return
		}
		in.Limit = n
	}
	if raw := q.Get("archived"); raw != "" {
		v, err := strconv.ParseBool(raw)
		if err != nil {
			writeError(w, 400, "validation_failed", "archived 必须为 true 或 false")
			return
		}
		in.Archived = &v
	}
	v, err := s.service.ListCases(r.Context(), in)
	if err != nil {
		handleErr(w, err)
		return
	}
	writeJSON(w, 200, v)
}
func (s *Server) CorrectQualificationHandler(w http.ResponseWriter, r *http.Request) {
	run(s, w, r, func(in application.QualificationCorrectionInput) (application.Result, error) {
		return s.service.CorrectQualification(r.Context(), r.PathValue("case_id"), r.PathValue("qualification_id"), in)
	})
}
func (s *Server) QualificationVersionsHandler(w http.ResponseWriter, r *http.Request) {
	v, err := s.service.QualificationVersions(r.Context(), r.PathValue("case_id"), r.PathValue("qualification_id"))
	if err != nil {
		handleErr(w, err)
		return
	}
	writeJSON(w, 200, v)
}
func (s *Server) DeviationActionQueueHandler(w http.ResponseWriter, r *http.Request) {
	raw := r.URL.Query().Get("as_of")
	at, err := time.Parse(time.RFC3339Nano, raw)
	if err != nil {
		writeError(w, 400, "validation_failed", "as_of 必须为 RFC3339 时刻")
		return
	}
	v, err := s.service.DeviationActionQueue(r.Context(), r.PathValue("case_id"), at)
	if err != nil {
		handleErr(w, err)
		return
	}
	writeJSON(w, 200, v)
}
func (s *Server) DueDateRevisionHandler(w http.ResponseWriter, r *http.Request) {
	run(s, w, r, func(in application.DueDateRevisionInput) (application.Result, error) {
		return s.service.ReviseDueDate(r.Context(), r.PathValue("case_id"), r.PathValue("deviation_id"), in)
	})
}
func (s *Server) ReviewPreflightHandler(w http.ResponseWriter, r *http.Request) {
	reviewer := strings.TrimSpace(r.URL.Query().Get("reviewer_id"))
	v, err := s.service.ReviewPreflight(r.Context(), r.PathValue("case_id"), reviewer)
	if err != nil {
		handleErr(w, err)
		return
	}
	writeJSON(w, 200, v)
}
func (s *Server) TrialProgressHandler(w http.ResponseWriter, r *http.Request) {
	v, err := s.service.TrialProgress(r.Context(), r.PathValue("case_id"))
	if err != nil {
		handleErr(w, err)
		return
	}
	writeJSON(w, 200, v)
}
func (s *Server) ArchiveVerificationHandler(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	v, err := s.service.VerifyArchive(r.Context(), r.PathValue("case_id"), q.Get("archive_digest"), q.Get("kind"), q.Get("id"))
	if err != nil {
		handleErr(w, err)
		return
	}
	writeJSON(w, 200, v)
}
