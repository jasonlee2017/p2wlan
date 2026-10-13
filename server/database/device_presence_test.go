package database

import (
	"os"
	"testing"
	"time"
)

func TestDevicePresenceTracksSessionsAndStaleLease(t *testing.T) {
	path := "test_device_presence.db"
	defer os.Remove(path)
	defer os.Remove(path + "-shm")
	defer os.Remove(path + "-wal")
	db, err := New(path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	user, err := db.CreateUser("presence@example.test", "password")
	if err != nil {
		t.Fatal(err)
	}
	device, err := db.CreateDevice(user.ID, "default", "presence-public-key", "Presence", "windows", "")
	if err != nil {
		t.Fatal(err)
	}

	now := time.Now().Unix()
	started := now - 20
	if _, err := db.Exec(`UPDATE devices SET last_seen = ?, online = 1 WHERE id = ?`, now, device.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`UPDATE device_presence SET online_since = ?, total_online_seconds = 7, online_session_count = 1 WHERE device_id = ?`, started, device.ID); err != nil {
		t.Fatal(err)
	}

	if err := db.ReleaseDevicePresence(device.ID); err != nil {
		t.Fatal(err)
	}
	presence, err := db.DevicePresence(device.ID)
	if err != nil {
		t.Fatal(err)
	}
	if presence.OnlineSince != 0 || presence.TotalOnlineSeconds < 27 || presence.OnlineSessionCount != 1 {
		t.Fatalf("unexpected closed session: %+v", presence)
	}

	if err := db.UpdateDeviceEndpoint(device.ID, "", "unknown", nil); err != nil {
		t.Fatal(err)
	}
	presence, err = db.DevicePresence(device.ID)
	if err != nil {
		t.Fatal(err)
	}
	if presence.OnlineSince == 0 || presence.OnlineSessionCount != 2 {
		t.Fatalf("expected a new session after reconnect: %+v", presence)
	}

	staleSeen := time.Now().Unix() - DeviceOnlineTTL - 5
	if _, err := db.Exec(`UPDATE devices SET last_seen = ?, online = 1 WHERE id = ?`, staleSeen, device.ID); err != nil {
		t.Fatal(err)
	}
	if err := db.MarkStaleDevicesOffline(DeviceOnlineTTL); err != nil {
		t.Fatal(err)
	}
	presence, err = db.DevicePresence(device.ID)
	if err != nil {
		t.Fatal(err)
	}
	if presence.OnlineSince != 0 {
		t.Fatalf("stale lease must close the session: %+v", presence)
	}
}
