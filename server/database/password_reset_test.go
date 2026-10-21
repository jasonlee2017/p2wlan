package database

import (
	"errors"
	"sync"
	"testing"
	"time"
)

func TestPasswordResetCodeIsOneTimeAndExpires(t *testing.T) {
	db, err := New(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	user, err := db.CreateUser("reset@example.test", "old-hash")
	if err != nil {
		t.Fatal(err)
	}
	now := time.Unix(1_700_000_000, 0).UTC()
	code, expiresAt, err := db.IssuePasswordResetCode(user.ID, now)
	if err != nil {
		t.Fatal(err)
	}
	if expiresAt != now.Add(PasswordResetCodeTTL).Unix() {
		t.Fatalf("unexpected expiry: %d", expiresAt)
	}
	if err := db.ResetPassword(user.ID, code, "new-hash", now); err != nil {
		t.Fatal(err)
	}
	if err := db.ResetPassword(user.ID, code, "other-hash", now); !errors.Is(err, ErrPasswordResetInvalid) {
		t.Fatalf("reused code error = %v", err)
	}
	code, _, err = db.IssuePasswordResetCode(user.ID, now)
	if err != nil {
		t.Fatal(err)
	}
	if err := db.ResetPassword(user.ID, code, "expired-hash", now.Add(PasswordResetCodeTTL+time.Second)); !errors.Is(err, ErrPasswordResetExpired) {
		t.Fatalf("expired code error = %v", err)
	}
}

func TestPasswordResetWrongCodeAttemptsAreBounded(t *testing.T) {
	db, err := New(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	user, err := db.CreateUser("attempts@example.test", "old-hash")
	if err != nil {
		t.Fatal(err)
	}
	now := time.Unix(1_700_000_000, 0).UTC()
	if _, _, err := db.IssuePasswordResetCode(user.ID, now); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < PasswordResetMaxAttempts-1; i++ {
		if err := db.ResetPassword(user.ID, "wrong", "new-hash", now); !errors.Is(err, ErrPasswordResetInvalid) {
			t.Fatalf("attempt %d error = %v", i+1, err)
		}
	}
	if err := db.ResetPassword(user.ID, "wrong", "new-hash", now); !errors.Is(err, ErrPasswordResetAttempts) {
		t.Fatalf("last attempt error = %v", err)
	}
}

func TestPasswordResetConcurrentConsumptionHasSingleWinner(t *testing.T) {
	db, err := New(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	user, err := db.CreateUser("concurrent@example.test", "old-hash")
	if err != nil {
		t.Fatal(err)
	}
	now := time.Unix(1_700_000_000, 0).UTC()
	code, _, err := db.IssuePasswordResetCode(user.ID, now)
	if err != nil {
		t.Fatal(err)
	}
	results := make(chan error, 2)
	var wg sync.WaitGroup
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			results <- db.ResetPassword(user.ID, code, "new-hash", now)
		}()
	}
	wg.Wait()
	close(results)
	successes := 0
	for result := range results {
		if result == nil {
			successes++
		} else if !errors.Is(result, ErrPasswordResetInvalid) {
			t.Fatalf("concurrent loser error = %v", result)
		}
	}
	if successes != 1 {
		t.Fatalf("successes = %d, want 1", successes)
	}
}
