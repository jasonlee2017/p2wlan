package api

import (
	"encoding/json"
	"errors"
	"net/http"
	"strings"

	"github.com/yhan-sun/p2wlan/server/auth"
	"github.com/yhan-sun/p2wlan/server/database"
)

// ---- Auth endpoints ----

// Login handles POST /api/v1/login.
func (s *Server) Login(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Email      string `json:"email"`
		Identifier string `json:"identifier"`
		Password   string `json:"password"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, `{"error":"invalid request"}`, http.StatusBadRequest)
		return
	}

	identifier := strings.TrimSpace(req.Identifier)
	if identifier == "" {
		identifier = strings.TrimSpace(req.Email)
	}
	if isValidEmail(identifier) {
		identifier = strings.ToLower(identifier)
	}
	if !isValidLoginIdentifier(identifier) {
		http.Error(w, `{"error":"invalid email or username"}`, http.StatusBadRequest)
		return
	}
	if !isValidPassword(req.Password) {
		http.Error(w, `{"error":"invalid password"}`, http.StatusBadRequest)
		return
	}

	token, user, err := s.auth.Login(identifier, req.Password)
	if err != nil {
		if errors.Is(err, auth.ErrAccountPending) {
			writeJSON(w, http.StatusForbidden, map[string]any{"error": "account pending administrator activation", "error_code": "account_pending"})
			return
		}
		http.Error(w, `{"error":"invalid credentials"}`, http.StatusUnauthorized)
		return
	}

	writeJSON(w, http.StatusOK, map[string]interface{}{
		"success": true,
		"token":   token,
		"user":    user,
	})
}

// Register handles POST /api/v1/register.
func (s *Server) Register(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Email    string `json:"email"`
		Password string `json:"password"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, `{"error":"invalid request"}`, http.StatusBadRequest)
		return
	}

	req.Email = strings.TrimSpace(req.Email)
	req.Email = strings.ToLower(req.Email)
	if !isValidEmail(req.Email) {
		http.Error(w, `{"error":"invalid email"}`, http.StatusBadRequest)
		return
	}
	if !isValidPassword(req.Password) {
		http.Error(w, `{"error":"invalid password (min 6 characters)"}`, http.StatusBadRequest)
		return
	}

	_, user, err := s.auth.RegisterPending(req.Email, req.Password)
	if err != nil {
		http.Error(w, `{"error":"registration failed"}`, http.StatusInternalServerError)
		return
	}

	writeJSON(w, http.StatusAccepted, map[string]interface{}{
		"success":             true,
		"user":                user,
		"activation_required": true,
	})
}

// PasswordReset handles a user supplied administrator-issued one-time code.
// It deliberately accepts an email or username but never sends email or
// reveals whether the identifier exists.
func (s *Server) PasswordReset(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Identifier string `json:"identifier"`
		Email      string `json:"email"`
		Code       string `json:"code"`
		Password   string `json:"password"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, `{"error":"invalid request"}`, http.StatusBadRequest)
		return
	}
	identifier := strings.TrimSpace(req.Identifier)
	if identifier == "" {
		identifier = strings.TrimSpace(req.Email)
	}
	if isValidEmail(identifier) {
		identifier = strings.ToLower(identifier)
	}
	if !isValidLoginIdentifier(identifier) || strings.TrimSpace(req.Code) == "" || !isValidPassword(req.Password) {
		http.Error(w, `{"error":"invalid reset request"}`, http.StatusBadRequest)
		return
	}
	user, err := s.db.GetUserByLoginIdentifier(identifier)
	if err != nil {
		http.Error(w, `{"error":"reset code is invalid or expired","error_code":"password_reset_invalid"}`, http.StatusUnauthorized)
		return
	}
	if err := s.auth.ResetPassword(user.ID, req.Code, req.Password); err != nil {
		status := http.StatusUnauthorized
		code := "password_reset_invalid"
		switch {
		case errors.Is(err, database.ErrPasswordResetExpired):
			code = "password_reset_expired"
		case errors.Is(err, database.ErrPasswordResetAttempts):
			code = "password_reset_attempts_exceeded"
		case errors.Is(err, database.ErrPasswordResetInvalid):
			code = "password_reset_invalid"
		}
		writeJSON(w, status, map[string]string{"error": "reset code is invalid or expired", "error_code": code})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"success": true})
}

// ChangePassword changes the authenticated user's password.
func (s *Server) ChangePassword(w http.ResponseWriter, r *http.Request) {
	claims, err := auth.GetClaims(r.Context())
	if err != nil {
		http.Error(w, `{"error":"unauthorized"}`, http.StatusUnauthorized)
		return
	}
	var req struct {
		CurrentPassword string `json:"current_password"`
		Password        string `json:"password"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || !isValidPassword(req.Password) {
		http.Error(w, `{"error":"invalid password"}`, http.StatusBadRequest)
		return
	}
	if err := s.auth.ChangePassword(claims.UserID, req.CurrentPassword, req.Password); err != nil {
		if errors.Is(err, auth.ErrInvalidCredentials) {
			http.Error(w, `{"error":"current password is invalid"}`, http.StatusUnauthorized)
			return
		}
		http.Error(w, `{"error":"password change failed"}`, http.StatusInternalServerError)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"success": true})
}

// Profile uses an account JWT; device credentials cannot edit an account.
func (s *Server) Profile(w http.ResponseWriter, r *http.Request) {
	actor, ok := roomActor(w, r)
	if !ok {
		return
	}
	if r.Method == http.MethodPatch {
		var req struct {
			Username string `json:"username"`
		}
		if !roomBody(w, r, &req) {
			return
		}
		user, err := s.db.UpdateUsername(actor, req.Username)
		if errors.Is(err, database.ErrInvalidUsername) {
			http.Error(w, `{"error":"invalid username"}`, 400)
			return
		}
		if err != nil {
			http.Error(w, `{"error":"profile update failed"}`, 500)
			return
		}
		writeJSON(w, 200, map[string]any{"user": user})
		return
	}
	user, err := s.db.GetUserByID(actor)
	if err != nil {
		http.Error(w, `{"error":"account not found"}`, 404)
		return
	}
	writeJSON(w, 200, map[string]any{"user": user})
}
