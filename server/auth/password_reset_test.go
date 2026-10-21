package auth

import (
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/yhan-sun/p2wlan/server/database"
)

func TestPasswordResetInvalidatesExistingJWT(t *testing.T) {
	db, err := database.New(filepath.Join(t.TempDir(), "auth.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	service := NewService("password-reset-secret", db)
	token, user, err := service.Register("jwt-reset@example.test", "oldpass")
	if err != nil {
		t.Fatal(err)
	}
	code, _, err := db.IssuePasswordResetCode(user.ID, nowForPasswordResetTest())
	if err != nil {
		t.Fatal(err)
	}
	if err := service.ResetPassword(user.ID, code, "newpass"); err != nil {
		t.Fatal(err)
	}
	if _, err := service.ValidateToken(token); !errors.Is(err, ErrInvalidToken) {
		t.Fatalf("old token validation error = %v", err)
	}
	if _, _, err := service.Login("jwt-reset@example.test", "newpass"); err != nil {
		t.Fatalf("new password login failed: %v", err)
	}
}

func nowForPasswordResetTest() (now time.Time) {
	return time.Now().UTC()
}
