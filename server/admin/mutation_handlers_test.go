package admin

import (
	"database/sql"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

type mutationFakeStore struct {
	fakeStore
	accountID  string
	roomID     string
	deviceID   string
	accountErr error
	roomErr    error
	deviceErr  error
}

func (s *mutationFakeStore) DeleteAccount(id string) error {
	s.accountID = id
	return s.accountErr
}

func (s *mutationFakeStore) DeleteRoomAsAdmin(id string) ([]string, error) {
	s.roomID = id
	return nil, s.roomErr
}

func (s *mutationFakeStore) DeleteAdminDevice(id string) error {
	s.deviceID = id
	return s.deviceErr
}

func TestAdminDeleteRoutesRequireAuthAndForwardIDs(t *testing.T) {
	token := strings.Repeat("m", 32)
	store := &mutationFakeStore{}
	server, err := New(store, Config{Token: token})
	if err != nil {
		t.Fatal(err)
	}
	mux := http.NewServeMux()
	server.RegisterRoutes(mux)

	for _, path := range []string{
		"/admin/api/v1/accounts/account-1",
		"/admin/api/v1/rooms/room-1",
		"/admin/api/v1/devices/device-1",
	} {
		res := httptest.NewRecorder()
		mux.ServeHTTP(res, httptest.NewRequest(http.MethodDelete, path, nil))
		if res.Code != http.StatusUnauthorized {
			t.Fatalf("%s: expected 401, got %d", path, res.Code)
		}
	}

	for _, tc := range []struct {
		path string
		seen *string
	}{
		{path: "/admin/api/v1/accounts/account-1", seen: &store.accountID},
		{path: "/admin/api/v1/rooms/room-1", seen: &store.roomID},
		{path: "/admin/api/v1/devices/device-1", seen: &store.deviceID},
	} {
		req := httptest.NewRequest(http.MethodDelete, tc.path, nil)
		req.Header.Set("Authorization", "Bearer "+token)
		res := httptest.NewRecorder()
		mux.ServeHTTP(res, req)
		if res.Code != http.StatusOK || !strings.Contains(res.Body.String(), `"success":true`) {
			t.Fatalf("%s: expected successful delete, got %d: %s", tc.path, res.Code, res.Body.String())
		}
		if *tc.seen == "" {
			t.Fatalf("%s: path ID was not forwarded", tc.path)
		}
	}
}

func TestAdminDeleteRoutesMapMissingResource(t *testing.T) {
	token := strings.Repeat("n", 32)
	store := &mutationFakeStore{accountErr: sql.ErrNoRows}
	server, err := New(store, Config{Token: token})
	if err != nil {
		t.Fatal(err)
	}
	mux := http.NewServeMux()
	server.RegisterRoutes(mux)
	req := httptest.NewRequest(http.MethodDelete, "/admin/api/v1/accounts/missing", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	res := httptest.NewRecorder()
	mux.ServeHTTP(res, req)
	if res.Code != http.StatusNotFound || !strings.Contains(res.Body.String(), `"error":"account not found"`) {
		t.Fatalf("expected missing account response, got %d: %s", res.Code, res.Body.String())
	}
}
