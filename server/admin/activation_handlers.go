package admin

import (
	"database/sql"
	"encoding/json"
	"net/http"
)

func activationValue(w http.ResponseWriter, r *http.Request) (bool, bool) {
	var req struct { Active *bool `json:"active"` }
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096)).Decode(&req); err != nil || req.Active == nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "active must be a boolean"})
		return false, false
	}
	return *req.Active, true
}

func (s *Server) accountActivation(w http.ResponseWriter, r *http.Request) {
	active, ok := activationValue(w, r); if !ok { return }
	store, ok := s.store.(interface{ SetAccountActive(string, bool) error }); if !ok { writeJSON(w, http.StatusNotImplemented, map[string]string{"error":"activation is unavailable"}); return }
	if err := store.SetAccountActive(r.PathValue("id"), active); err != nil {
		if err == sql.ErrNoRows { writeJSON(w, http.StatusNotFound, map[string]string{"error": "account not found"}); return }
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "unable to update account activation"}); return
	}
	writeJSON(w, http.StatusOK, map[string]any{"success": true, "active": active})
}

func (s *Server) roomActivation(w http.ResponseWriter, r *http.Request) {
	active, ok := activationValue(w, r); if !ok { return }
	store, ok := s.store.(interface{ SetAdminRoomActive(string, bool) error }); if !ok { writeJSON(w, http.StatusNotImplemented, map[string]string{"error":"activation is unavailable"}); return }
	if err := store.SetAdminRoomActive(r.PathValue("id"), active); err != nil {
		if err == sql.ErrNoRows { writeJSON(w, http.StatusNotFound, map[string]string{"error": "room not found"}); return }
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "unable to update room activation"}); return
	}
	writeJSON(w, http.StatusOK, map[string]any{"success": true, "active": active})
}
