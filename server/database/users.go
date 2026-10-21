package database

import (
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"
)

// ---- User operations ----

// User represents a registered user.
type User struct {
	Username        string `json:"username"`
	ID              string `json:"id"`
	Email           string `json:"email"`
	PasswordHash    string `json:"-"`
	CreatedAt       int64  `json:"created_at"`
	Active          bool   `json:"active"`
	PasswordVersion int64  `json:"-"`
}

var ErrAccountPending = errors.New("account is pending administrator activation")
var ErrAccountProtected = errors.New("system account cannot be deleted")

// Network represents a virtual network.
type Network struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	CIDR      string `json:"cidr"`
	OwnerID   string `json:"owner_id"`
	CreatedAt int64  `json:"created_at"`
}

// CreateUser inserts a new user.
func (db *DB) CreateUser(email, passwordHash string) (*User, error) {
	return db.createUser(email, passwordHash, true)
}

func (db *DB) CreatePendingUser(email, passwordHash string) (*User, error) {
	return db.createUser(email, passwordHash, false)
}

func (db *DB) createUser(email, passwordHash string, active bool) (*User, error) {
	id, err := roomRandomID("user-", 16)
	if err != nil {
		return nil, fmt.Errorf("generate user ID: %w", err)
	}
	membershipID, err := roomRandomID("mem-", 16)
	if err != nil {
		return nil, fmt.Errorf("generate membership ID: %w", err)
	}
	tx, err := db.Begin()
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	now := time.Now().Unix()

	activeValue := 0
	if active {
		activeValue = 1
	}
	_, err = tx.Exec(`INSERT INTO users (id, email, password_hash, created_at, active) VALUES (?, ?, ?, ?, ?)`, id, email, passwordHash, now, activeValue)
	if err != nil {
		return nil, err
	}

	// Keep the legacy default-network membership so existing clients can
	// register without a migration. Membership grants network registration
	// access; it does not grant visibility or management rights over another
	// account's devices.
	if _, err := tx.Exec(`INSERT INTO network_memberships (id, user_id, network_id, role, created_at)
		VALUES (?, ?, 'default', 'member', ?)`, membershipID, id, now); err != nil {
		return nil, fmt.Errorf("create default membership: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return &User{ID: id, Email: email, PasswordHash: passwordHash, CreatedAt: now, Active: active, PasswordVersion: 1}, nil
}

// GetUserByEmail looks up a user by email.
func (db *DB) GetUserByEmail(email string) (*User, error) {
	var u User
	err := db.QueryRow(`SELECT id, email, password_hash, created_at, username, active, password_version FROM users WHERE email = ?`, email).
		Scan(&u.ID, &u.Email, &u.PasswordHash, &u.CreatedAt, &u.Username, &u.Active, &u.PasswordVersion)
	if err != nil {
		return nil, err
	}
	return &u, nil
}

// GetUserByLoginIdentifier resolves either the account email or its optional
// display username. Usernames are not unique in legacy databases, so an
// ambiguous username is rejected rather than selecting an arbitrary account.
func (db *DB) GetUserByLoginIdentifier(identifier string) (*User, error) {
	identifier = strings.TrimSpace(identifier)
	if identifier == "" {
		return nil, sql.ErrNoRows
	}
	if user, err := db.GetUserByEmail(strings.ToLower(identifier)); err == nil {
		return user, nil
	}

	rows, err := db.Query(`
		SELECT id, email, password_hash, created_at, username, active, password_version
		FROM users
		WHERE username = ?
		LIMIT 2`, identifier)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var match *User
	count := 0
	for rows.Next() {
		var user User
		if err := rows.Scan(
			&user.ID,
			&user.Email,
			&user.PasswordHash,
			&user.CreatedAt,
			&user.Username,
			&user.Active,
			&user.PasswordVersion,
		); err != nil {
			return nil, err
		}
		count++
		if count == 1 {
			match = &user
		}
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if count != 1 {
		return nil, sql.ErrNoRows
	}
	return match, nil
}

var ErrInvalidUsername = errors.New("username must contain 1–32 characters without control characters")

func ValidUsername(name string) bool {
	if !utf8.ValidString(name) || utf8.RuneCountInString(name) < 1 || utf8.RuneCountInString(name) > 32 {
		return false
	}
	for _, c := range name {
		if unicode.IsControl(c) || unicode.In(c, unicode.Cf) {
			return false
		}
	}
	return strings.TrimSpace(name) == name
}

func (db *DB) GetUserByID(id string) (*User, error) {
	var u User
	err := db.QueryRow(`SELECT id, email, username, created_at, active, password_version FROM users WHERE id = ?`, id).Scan(&u.ID, &u.Email, &u.Username, &u.CreatedAt, &u.Active, &u.PasswordVersion)
	return &u, err
}

// GetUserByIDWithPasswordVersion returns the account fields needed to fence a JWT.
func (db *DB) GetUserByIDWithPasswordVersion(id string) (*User, error) {
	return db.GetUserByID(id)
}

// GetUserAuthByID returns the password hash and fencing version for password
// changes. It is intentionally not used by profile or admin resource reads.
func (db *DB) GetUserAuthByID(id string) (*User, error) {
	var u User
	err := db.QueryRow(`SELECT id, email, password_hash, created_at, username, active, password_version FROM users WHERE id = ?`, id).
		Scan(&u.ID, &u.Email, &u.PasswordHash, &u.CreatedAt, &u.Username, &u.Active, &u.PasswordVersion)
	return &u, err
}

func (db *DB) SetAccountActive(id string, active bool) error {
	value := 0
	if active {
		value = 1
	}
	result, err := db.Exec(`UPDATE users SET active = ? WHERE id = ? AND id <> 'system'`, value, strings.TrimSpace(id))
	if err != nil {
		return err
	}
	count, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if count == 0 {
		return sql.ErrNoRows
	}
	return nil
}

// DeleteAccount removes an account and every resource it owns. Devices are
// revoked first so credentials, tunnels, signals, and relay access cannot
// survive the account deletion; room subnets are returned to the allocator.
func (db *DB) DeleteAccount(id string) error {
	id = strings.TrimSpace(id)
	if id == "" || id == "system" {
		return ErrAccountProtected
	}
	tx, err := db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var exists bool
	if err := tx.QueryRow(`SELECT EXISTS(SELECT 1 FROM users WHERE id = ?)`, id).Scan(&exists); err != nil {
		return err
	}
	if !exists {
		return sql.ErrNoRows
	}
	deviceRows, err := tx.Query(`SELECT id FROM devices WHERE user_id = ?`, id)
	if err != nil {
		return err
	}
	var deviceIDs []string
	for deviceRows.Next() {
		var deviceID string
		if err := deviceRows.Scan(&deviceID); err != nil {
			deviceRows.Close()
			return err
		}
		deviceIDs = append(deviceIDs, deviceID)
	}
	if err := deviceRows.Close(); err != nil {
		return err
	}
	for _, deviceID := range deviceIDs {
		if err := revokeRoomDeviceTx(tx, deviceID, true); err != nil {
			return err
		}
	}
	ownedNetworks, err := tx.Query(`SELECT id FROM networks WHERE owner_id = ? AND id <> 'default'`, id)
	if err != nil {
		return err
	}
	var networkIDs []string
	for ownedNetworks.Next() {
		var networkID string
		if err := ownedNetworks.Scan(&networkID); err != nil {
			ownedNetworks.Close()
			return err
		}
		networkIDs = append(networkIDs, networkID)
	}
	if err := ownedNetworks.Close(); err != nil {
		return err
	}
	for _, networkID := range networkIDs {
		otherDevices, err := tx.Query(`SELECT id FROM devices WHERE network_id = ?`, networkID)
		if err != nil {
			return err
		}
		var ids []string
		for otherDevices.Next() {
			var deviceID string
			if err := otherDevices.Scan(&deviceID); err != nil {
				otherDevices.Close()
				return err
			}
			ids = append(ids, deviceID)
		}
		if err := otherDevices.Close(); err != nil {
			return err
		}
		for _, deviceID := range ids {
			if err := revokeRoomDeviceTx(tx, deviceID, true); err != nil {
				return err
			}
		}
		for _, query := range []string{
			`UPDATE room_subnets SET network_id = NULL, reusable_after = ? WHERE network_id = ?`,
			`DELETE FROM room_device_settings WHERE network_id = ?`,
			`DELETE FROM room_device_access WHERE network_id = ?`,
			`DELETE FROM room_bans WHERE network_id = ?`,
			`DELETE FROM room_invites WHERE network_id = ?`,
			`DELETE FROM rooms WHERE network_id = ?`,
			`DELETE FROM network_memberships WHERE network_id = ?`,
			`DELETE FROM networks WHERE id = ?`,
		} {
			args := []any{networkID}
			if strings.HasPrefix(query, "UPDATE room_subnets") {
				args = []any{time.Now().Unix() + roomSubnetQuarantineSeconds, networkID}
			}
			if _, err := tx.Exec(query, args...); err != nil {
				return err
			}
		}
	}
	if _, err := tx.Exec(`DELETE FROM network_memberships WHERE user_id = ?`, id); err != nil {
		return err
	}
	// A room device access record belongs to the room but also references the
	// account. Remove access records from rooms owned by other accounts before
	// deleting the user; those rooms cannot be removed as part of this account.
	if _, err := tx.Exec(`DELETE FROM room_device_access WHERE user_id = ?`, id); err != nil {
		return err
	}
	if _, err := tx.Exec(`DELETE FROM room_bans WHERE user_id = ?`, id); err != nil {
		return err
	}
	if _, err := tx.Exec(`DELETE FROM room_join_limits WHERE user_id = ?`, id); err != nil {
		return err
	}
	if _, err := tx.Exec(`DELETE FROM users WHERE id = ?`, id); err != nil {
		return err
	}
	return tx.Commit()
}

func (db *DB) IsAccountActive(id string) (bool, error) {
	var active bool
	err := db.QueryRow(`SELECT active FROM users WHERE id = ?`, id).Scan(&active)
	return active, err
}

func (db *DB) UpdateUsername(id, name string) (*User, error) {
	name = strings.TrimSpace(name)
	if !ValidUsername(name) {
		return nil, ErrInvalidUsername
	}
	if _, err := db.Exec(`UPDATE users SET username = ? WHERE id = ?`, name, id); err != nil {
		return nil, err
	}
	return db.GetUserByID(id)
}
