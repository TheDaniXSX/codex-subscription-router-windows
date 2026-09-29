package control

import (
	"errors"
	"net/http"
	"strings"
	"unicode"

	"github.com/TheDaniXSX/codex-subscription-router-windows/internal/usage"
)

func usageID(s string) bool {
	return s != "" && len(s) <= 256 && strings.TrimSpace(s) == s && strings.IndexFunc(s, unicode.IsControl) < 0
}

func (s *Server) usageTurn(w http.ResponseWriter, r *http.Request) {
	if !s.authorized(r) {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "unauthorized"})
		return
	}
	if r.Method != http.MethodGet {
		methodNotAllowed(w)
		return
	}
	q := r.URL.Query()
	if len(q["threadId"]) != 1 || len(q["turnId"]) != 1 || !usageID(q.Get("threadId")) || !usageID(q.Get("turnId")) {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "one valid threadId and turnId are required"})
		return
	}
	view, err := s.mux.UsageTurn(r.Context(), q.Get("threadId"), q.Get("turnId"))
	if err != nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "usage history unavailable"})
		return
	}
	writeJSON(w, http.StatusOK, view)
}

func (s *Server) usageStatus(w http.ResponseWriter, r *http.Request) {
	if !s.authorized(r) {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "unauthorized"})
		return
	}
	if r.Method != http.MethodGet {
		methodNotAllowed(w)
		return
	}
	status, err := s.mux.UsageStatus(r.Context())
	if err != nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "usage history unavailable"})
		return
	}
	writeJSON(w, http.StatusOK, status)
}

func (s *Server) usageCalibration(w http.ResponseWriter, r *http.Request) {
	if !s.authorized(r) {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "unauthorized"})
		return
	}
	if r.Method != http.MethodPatch {
		methodNotAllowed(w)
		return
	}
	var change struct {
		RootID         string  `json:"rootId"`
		Included       *bool   `json:"included"`
		Revision       *uint64 `json:"revision"`
		IncludeRelated bool    `json:"includeRelated,omitempty"`
	}
	if err := decodeJSON(r, &change); err != nil || !usageID(change.RootID) || change.Included == nil || change.Revision == nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "rootId, included and revision are required"})
		return
	}
	view, err := s.mux.SetUsageCalibration(r.Context(), change.RootID, *change.Included, *change.Revision, change.IncludeRelated)
	if errors.Is(err, usage.ErrRevisionConflict) {
		writeJSON(w, http.StatusConflict, map[string]string{"error": "usage changed; refresh before retrying"})
		return
	}
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, view)
}
