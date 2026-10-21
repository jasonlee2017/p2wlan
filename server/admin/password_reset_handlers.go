package admin

import (
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"time"
)

func (s *Server) passwordResetCode(w http.ResponseWriter, r *http.Request) {
	store, ok := s.store.(interface {
		IssuePasswordResetCode(string, time.Time) (string, int64, error)
	})
	if !ok {
		writeJSON(w, http.StatusNotImplemented, map[string]string{"error": "password reset is unavailable"})
		return
	}
	var req struct {
		Confirm bool `json:"confirm"`
	}
	if r.Body == nil || json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096)).Decode(&req) != nil || !req.Confirm {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "confirm must be true"})
		return
	}
	code, expiresAt, err := store.IssuePasswordResetCode(r.PathValue("id"), time.Now().UTC())
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			writeJSON(w, http.StatusNotFound, map[string]string{"error": "account not found"})
			return
		}
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "unable to create password reset code"})
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{
		"success":    true,
		"code":       code,
		"expires_at": expiresAt,
		"warning":    "显示后不会再次返回；请通过可信的站外渠道交给账号所有者。",
	})
}
