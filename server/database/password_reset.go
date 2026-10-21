package database

import (
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"time"
)

const (
	PasswordResetCodeTTL      = 15 * time.Minute
	PasswordResetMaxAttempts  = 5
	passwordResetCodeByteSize = 32
)

var (
	ErrPasswordResetInvalid  = errors.New("password reset code is invalid")
	ErrPasswordResetExpired  = errors.New("password reset code is expired")
	ErrPasswordResetAttempts = errors.New("password reset code attempts exceeded")
)

// IssuePasswordResetCode revokes previous codes for the account and returns a
// new one-time code. Only the returned plaintext is shown to the administrator;
// the database stores its SHA-256 hash.
func (db *DB) IssuePasswordResetCode(userID string, now time.Time) (string, int64, error) {
	userID = strings.TrimSpace(userID)
	if userID == "" || userID == "system" {
		return "", 0, sql.ErrNoRows
	}
	if now.IsZero() {
		now = time.Now().UTC()
	}
	raw := make([]byte, passwordResetCodeByteSize)
	if _, err := rand.Read(raw); err != nil {
		return "", 0, fmt.Errorf("generate password reset code: %w", err)
	}
	code := "prw-" + hex.EncodeToString(raw)
	hash := sha256.Sum256([]byte(code))
	id, err := roomRandomID("reset-", 16)
	if err != nil {
		return "", 0, err
	}
	expiresAt := now.Add(PasswordResetCodeTTL).Unix()
	tx, err := db.Begin()
	if err != nil {
		return "", 0, err
	}
	defer tx.Rollback()
	var exists int
	if err := tx.QueryRow(`SELECT 1 FROM users WHERE id = ? AND id <> 'system'`, userID).Scan(&exists); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return "", 0, sql.ErrNoRows
		}
		return "", 0, err
	}
	if _, err := tx.Exec(`UPDATE password_reset_codes SET consumed = 1 WHERE user_id = ? AND consumed = 0`, userID); err != nil {
		return "", 0, err
	}
	if _, err := tx.Exec(`DELETE FROM password_reset_codes WHERE user_id = ? AND (consumed = 1 OR expires_at < ?)`, userID, now.Unix()); err != nil {
		return "", 0, err
	}
	if _, err := tx.Exec(`INSERT INTO password_reset_codes (id, user_id, token_hash, expires_at, attempts, consumed, created_at) VALUES (?, ?, ?, ?, 0, 0, ?)`, id, userID, hash[:], expiresAt, now.Unix()); err != nil {
		return "", 0, err
	}
	if err := tx.Commit(); err != nil {
		return "", 0, err
	}
	return code, expiresAt, nil
}

// ResetPassword atomically consumes a valid reset code and changes the account
// password. Device credentials are intentionally left untouched.
func (db *DB) ResetPassword(userID, code, passwordHash string, now time.Time) error {
	userID = strings.TrimSpace(userID)
	code = strings.TrimSpace(code)
	if userID == "" || code == "" || passwordHash == "" {
		return ErrPasswordResetInvalid
	}
	if now.IsZero() {
		now = time.Now().UTC()
	}
	hash := sha256.Sum256([]byte(code))
	tx, err := db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var resetID string
	var storedHash []byte
	var expiresAt int64
	var attempts, consumed int
	err = tx.QueryRow(`SELECT id, token_hash, expires_at, attempts, consumed FROM password_reset_codes WHERE user_id = ? AND consumed = 0 ORDER BY created_at DESC LIMIT 1`, userID).Scan(&resetID, &storedHash, &expiresAt, &attempts, &consumed)
	if errors.Is(err, sql.ErrNoRows) {
		return ErrPasswordResetInvalid
	}
	if err != nil {
		return err
	}
	if consumed != 0 || len(storedHash) != sha256.Size {
		return ErrPasswordResetInvalid
	}
	if expiresAt < now.Unix() {
		return ErrPasswordResetExpired
	}
	if attempts >= PasswordResetMaxAttempts {
		return ErrPasswordResetAttempts
	}
	if subtle.ConstantTimeCompare(storedHash, hash[:]) != 1 {
		attempts++
		consumedValue := 0
		if attempts >= PasswordResetMaxAttempts {
			consumedValue = 1
		}
		if _, err := tx.Exec(`UPDATE password_reset_codes SET attempts = ?, consumed = ? WHERE id = ? AND consumed = 0`, attempts, consumedValue, resetID); err != nil {
			return err
		}
		if err := tx.Commit(); err != nil {
			return err
		}
		if consumedValue != 0 {
			return ErrPasswordResetAttempts
		}
		return ErrPasswordResetInvalid
	}
	result, err := tx.Exec(`UPDATE password_reset_codes SET consumed = 1, attempts = attempts + 1 WHERE id = ? AND consumed = 0 AND attempts < ? AND expires_at >= ?`, resetID, PasswordResetMaxAttempts, now.Unix())
	if err != nil {
		return err
	}
	changed, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if changed != 1 {
		return ErrPasswordResetInvalid
	}
	userResult, err := tx.Exec(`UPDATE users SET password_hash = ?, password_version = password_version + 1 WHERE id = ? AND id <> 'system'`, passwordHash, userID)
	if err != nil {
		return err
	}
	updated, err := userResult.RowsAffected()
	if err != nil {
		return err
	}
	if updated != 1 {
		return sql.ErrNoRows
	}
	if err := tx.Commit(); err != nil {
		return err
	}
	return nil
}

// SetUserPassword changes a password for an authenticated account and bumps
// its password version, invalidating all previously issued JWTs.
func (db *DB) SetUserPassword(userID, passwordHash string) error {
	result, err := db.Exec(`UPDATE users SET password_hash = ?, password_version = password_version + 1 WHERE id = ? AND id <> 'system'`, passwordHash, strings.TrimSpace(userID))
	if err != nil {
		return err
	}
	count, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if count != 1 {
		return sql.ErrNoRows
	}
	return nil
}
