package database

import (
	"database/sql"
)

// DevicePresenceSummary is the durable session accounting owned by the
// control-plane lease. online_since is zero when no session is open.
type DevicePresenceSummary struct {
	OnlineSince        int64
	TotalOnlineSeconds int64
	OnlineSessionCount int64
	LastOnlineAt       int64
}

func ensureDevicePresenceTx(tx *sql.Tx, deviceID string, now int64, online bool) error {
	if online {
		_, err := tx.Exec(`INSERT OR IGNORE INTO device_presence (device_id, online_since, online_session_count, last_online_at) VALUES (?, ?, 1, ?)`, deviceID, now, now)
		return err
	}
	_, err := tx.Exec(`INSERT OR IGNORE INTO device_presence (device_id) VALUES (?)`, deviceID)
	return err
}

// startDevicePresenceTx closes an expired session before opening a new one.
// The previous session ends at its last accepted heartbeat, not at the later
// recovery request, so a long offline interval is never counted as online.
func startDevicePresenceTx(tx *sql.Tx, deviceID string, now, previousLastSeen int64) error {
	var presence DevicePresenceSummary
	err := tx.QueryRow(`SELECT online_since, total_online_seconds, online_session_count, last_online_at FROM device_presence WHERE device_id = ?`, deviceID).
		Scan(&presence.OnlineSince, &presence.TotalOnlineSeconds, &presence.OnlineSessionCount, &presence.LastOnlineAt)
	if err == sql.ErrNoRows {
		return ensureDevicePresenceTx(tx, deviceID, now, true)
	}
	if err != nil {
		return err
	}
	if presence.OnlineSince > 0 {
		end := previousLastSeen
		if end < presence.OnlineSince {
			end = presence.OnlineSince
		}
		presence.TotalOnlineSeconds += end - presence.OnlineSince
	}
	_, err = tx.Exec(`UPDATE device_presence SET online_since = ?, total_online_seconds = ?, online_session_count = ?, last_online_at = ? WHERE device_id = ?`,
		now, presence.TotalOnlineSeconds, presence.OnlineSessionCount+1, now, deviceID)
	return err
}

func touchDevicePresenceTx(tx *sql.Tx, deviceID string, now, previousLastSeen int64) error {
	var onlineSince int64
	err := tx.QueryRow(`SELECT online_since FROM device_presence WHERE device_id = ?`, deviceID).Scan(&onlineSince)
	if err == sql.ErrNoRows {
		return ensureDevicePresenceTx(tx, deviceID, now, true)
	}
	if err != nil {
		return err
	}
	if onlineSince <= 0 || previousLastSeen <= 0 || now-previousLastSeen > DeviceOnlineTTL {
		return startDevicePresenceTx(tx, deviceID, now, previousLastSeen)
	}
	return nil
}

func closeDevicePresenceTx(tx *sql.Tx, deviceID string, end int64) error {
	var onlineSince, total int64
	err := tx.QueryRow(`SELECT online_since, total_online_seconds FROM device_presence WHERE device_id = ?`, deviceID).Scan(&onlineSince, &total)
	if err == sql.ErrNoRows {
		return ensureDevicePresenceTx(tx, deviceID, end, false)
	}
	if err != nil {
		return err
	}
	if onlineSince > 0 {
		if end < onlineSince {
			end = onlineSince
		}
		total += end - onlineSince
	}
	_, err = tx.Exec(`UPDATE device_presence SET online_since = 0, total_online_seconds = ?, last_online_at = CASE WHEN ? > 0 THEN ? ELSE last_online_at END WHERE device_id = ?`, total, end, end, deviceID)
	return err
}

func (db *DB) DevicePresence(deviceID string) (*DevicePresenceSummary, error) {
	var presence DevicePresenceSummary
	err := db.QueryRow(`SELECT online_since, total_online_seconds, online_session_count, last_online_at FROM device_presence WHERE device_id = ?`, deviceID).
		Scan(&presence.OnlineSince, &presence.TotalOnlineSeconds, &presence.OnlineSessionCount, &presence.LastOnlineAt)
	if err != nil {
		return nil, err
	}
	return &presence, nil
}

func markStaleDevicePresenceTx(tx *sql.Tx, cutoff int64) error {
	rows, err := tx.Query(`SELECT d.id, d.last_seen, p.online_since, p.total_online_seconds FROM devices d JOIN device_presence p ON p.device_id = d.id WHERE d.online = 1 AND d.last_seen > 0 AND d.last_seen < ? AND p.online_since > 0`, cutoff)
	if err != nil {
		return err
	}
	defer rows.Close()
	type stale struct {
		id                     string
		lastSeen, since, total int64
	}
	var items []stale
	for rows.Next() {
		var item stale
		if err := rows.Scan(&item.id, &item.lastSeen, &item.since, &item.total); err != nil {
			return err
		}
		items = append(items, item)
	}
	if err := rows.Err(); err != nil {
		return err
	}
	if err := rows.Close(); err != nil {
		return err
	}
	for _, item := range items {
		end := item.lastSeen
		if end < item.since {
			end = item.since
		}
		if _, err := tx.Exec(`UPDATE device_presence SET online_since = 0, total_online_seconds = ? WHERE device_id = ?`, item.total+end-item.since, item.id); err != nil {
			return err
		}
	}
	return nil
}

func effectivePresence(summary DevicePresenceSummary, online bool, lastSeen, now int64) (current, total int64) {
	total = summary.TotalOnlineSeconds
	if summary.OnlineSince <= 0 {
		return 0, total
	}
	end := now
	if !online {
		end = lastSeen
		if end < summary.OnlineSince {
			end = summary.OnlineSince
		}
		closed := end - summary.OnlineSince
		if closed < 0 {
			closed = 0
		}
		return 0, total + closed
	}
	current = end - summary.OnlineSince
	if current < 0 {
		current = 0
	}
	return current, total + current
}
