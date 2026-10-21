package api

import (
	"bytes"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	"github.com/yhan-sun/p2wlan/server/auth"
	"github.com/yhan-sun/p2wlan/server/database"
)

func TestLoginAcceptsEmailAndAccountUsername(t *testing.T) {
	db, err := database.New(filepath.Join(t.TempDir(), "auth-handlers.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	service := auth.NewService("auth-handler-secret", db)
	_, user, err := service.Register("pyu@example.test", "password123")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.UpdateUsername(user.ID, "pyu"); err != nil {
		t.Fatal(err)
	}
	s := NewServer(service, nil, db)

	login := func(payload map[string]string) (int, map[string]any) {
		body, _ := json.Marshal(payload)
		r := httptest.NewRequest(http.MethodPost, "/api/v1/login", bytes.NewReader(body))
		w := httptest.NewRecorder()
		s.Login(w, r)
		var decoded map[string]any
		_ = json.Unmarshal(w.Body.Bytes(), &decoded)
		return w.Code, decoded
	}

	if code, body := login(map[string]string{"email": "pyu@example.test", "password": "password123"}); code != http.StatusOK || body["token"] == "" {
		t.Fatalf("email login failed: code=%d body=%v", code, body)
	}
	if code, body := login(map[string]string{"email": "pyu", "password": "password123"}); code != http.StatusOK || body["token"] == "" {
		t.Fatalf("username login failed: code=%d body=%v", code, body)
	}
	if code, body := login(map[string]string{"identifier": "pyu", "password": "password123"}); code != http.StatusOK || body["token"] == "" {
		t.Fatalf("identifier login failed: code=%d body=%v", code, body)
	}
}

func TestPasswordResetEndpointConsumesCodeAndInvalidatesToken(t *testing.T) {
	db, err := database.New(filepath.Join(t.TempDir(), "password-reset.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	service := auth.NewService("password-reset-api-secret", db)
	oldToken, user, err := service.Register("reset-api@example.test", "oldpass")
	if err != nil {
		t.Fatal(err)
	}
	code, _, err := db.IssuePasswordResetCode(user.ID, time.Now().UTC())
	if err != nil {
		t.Fatal(err)
	}
	s := NewServer(service, nil, db)
	body, _ := json.Marshal(map[string]string{
		"identifier": "reset-api@example.test",
		"code":       code,
		"password":   "newpass",
	})
	r := httptest.NewRequest(http.MethodPost, "/api/v1/password-reset", bytes.NewReader(body))
	w := httptest.NewRecorder()
	s.PasswordReset(w, r)
	if w.Code != http.StatusOK {
		t.Fatalf("reset status = %d, body = %s", w.Code, w.Body.String())
	}
	if _, err := service.ValidateToken(oldToken); !errors.Is(err, auth.ErrInvalidToken) {
		t.Fatalf("old token validation error = %v", err)
	}
	retry := httptest.NewRequest(http.MethodPost, "/api/v1/password-reset", bytes.NewReader(body))
	retryWriter := httptest.NewRecorder()
	s.PasswordReset(retryWriter, retry)
	if retryWriter.Code != http.StatusUnauthorized {
		t.Fatalf("reused code status = %d", retryWriter.Code)
	}
}
