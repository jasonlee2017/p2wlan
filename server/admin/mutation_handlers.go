package admin

import (
	"database/sql"
	"errors"
	"net/http"

	"github.com/yhan-sun/p2wlan/server/database"
)

func writeAdminDeleteError(w http.ResponseWriter, err error, resource string) {
	if errors.Is(err, sql.ErrNoRows) {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": resource + " not found"})
		return
	}
	writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "unable to delete " + resource})
}

func (s *Server) deleteAccount(w http.ResponseWriter, r *http.Request) {
	store, ok := s.store.(interface{ DeleteAccount(string) error })
	if !ok {
		writeJSON(w, http.StatusNotImplemented, map[string]string{"error": "account deletion is unavailable"})
		return
	}
	if err := store.DeleteAccount(r.PathValue("id")); err != nil {
		if errors.Is(err, database.ErrAccountProtected) {
			writeJSON(w, http.StatusForbidden, map[string]string{"error": "system account cannot be deleted"})
			return
		}
		writeAdminDeleteError(w, err, "account")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"success": true})
}

func (s *Server) deleteRoom(w http.ResponseWriter, r *http.Request) {
	store, ok := s.store.(interface{ DeleteRoomAsAdmin(string) ([]string, error) })
	if !ok {
		writeJSON(w, http.StatusNotImplemented, map[string]string{"error": "room deletion is unavailable"})
		return
	}
	if _, err := store.DeleteRoomAsAdmin(r.PathValue("id")); err != nil {
		writeAdminDeleteError(w, err, "room")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"success": true})
}

func (s *Server) deleteDevice(w http.ResponseWriter, r *http.Request) {
	store, ok := s.store.(interface{ DeleteAdminDevice(string) error })
	if !ok {
		writeJSON(w, http.StatusNotImplemented, map[string]string{"error": "device deletion is unavailable"})
		return
	}
	if err := store.DeleteAdminDevice(r.PathValue("id")); err != nil {
		writeAdminDeleteError(w, err, "device")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"success": true})
}
