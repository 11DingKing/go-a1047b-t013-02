package httpapi

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"arcticexpress/internal/booking"
	"arcticexpress/internal/payment"
	"arcticexpress/internal/storage"
	"arcticexpress/internal/store"
	"arcticexpress/internal/voyage"
)

type fakeClock struct{ t time.Time }

func (f *fakeClock) Now() time.Time { return f.t }

func newTestServer(t *testing.T) (*httptest.Server, *store.Store) {
	t.Helper()
	st, err := store.New("")
	if err != nil {
		t.Fatalf("store: %v", err)
	}
	clock := &fakeClock{t: time.Date(2026, 9, 1, 8, 0, 0, 0, time.UTC)}
	bs := booking.New(st, clock, booking.Config{
		HoldDuration:     2 * time.Hour,
		CutoffThreshold:  48 * time.Hour,
		PortChangeWindow: 72 * time.Hour,
	})
	vs := voyage.New(st, clock)
	ss := storage.New(st)
	ps := payment.New(st, clock)
	h := NewHandler(bs, vs, ss, ps)
	return httptest.NewServer(Router(h)), st
}

func doJSON(t *testing.T, srv *httptest.Server, method, path string, body any) (*http.Response, map[string]any) {
	t.Helper()
	var buf bytes.Buffer
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			t.Fatalf("marshal: %v", err)
		}
		buf.Write(b)
	}
	req, err := http.NewRequest(method, srv.URL+path, &buf)
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("do: %v", err)
	}
	defer resp.Body.Close()
	var result map[string]any
	_ = json.NewDecoder(resp.Body).Decode(&result)
	return resp, result
}

func TestHTTPFullBookingLifecycle(t *testing.T) {
	srv, _ := newTestServer(t)

	// Create voyage.
	departure := time.Date(2026, 10, 1, 8, 0, 0, 0, time.UTC)
	cutoff := time.Date(2026, 9, 25, 8, 0, 0, 0, time.UTC)
	resp, body := doJSON(t, srv, "POST", "/api/v1/voyages", map[string]any{
		"vessel_name":   "Arctic Sea",
		"voyage_number": "AE-001",
		"departure_at":  departure.Format(time.RFC3339),
		"cutoff_at":     cutoff.Format(time.RFC3339),
		"destinations":  []string{"Rotterdam", "Hamburg", "Gdynia"},
		"dg_capacity":   2,
		"gen_capacity":  2,
	})
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("create voyage status = %d, body = %v", resp.StatusCode, body)
	}
	voyageID := body["id"].(string)

	// Create storage location.
	resp, body = doJSON(t, srv, "POST", "/api/v1/storage-locations", map[string]any{"zone": "DG-A", "capacity": 2})
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("create storage status = %d", resp.StatusCode)
	}
	storageID := body["id"].(string)

	// Create booking.
	resp, body = doJSON(t, srv, "POST", "/api/v1/bookings", map[string]any{
		"idempotency_key": "IDEM-1",
		"cargo_owner_id":  "OWNER-1",
		"voyage_id":       voyageID,
		"container_id":    "CNTR-1",
		"cargo_type":      "battery",
		"description":     "power battery",
		"weight_kg":       1000,
		"un_number":       "UN3480",
		"destination":     "Rotterdam",
	})
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("create booking status = %d, body = %v", resp.StatusCode, body)
	}
	bookingID := body["id"].(string)
	if body["state"] != "initiated" {
		t.Fatalf("state = %v, want initiated", body["state"])
	}

	// Submit documents.
	cert := map[string]any{
		"id":            "DGC-1",
		"un_number":     "UN3480",
		"hazard_class":  "9",
		"packing_group": "II",
		"issued_at":     time.Now().Add(-24 * time.Hour).Format(time.RFC3339),
		"expires_at":    time.Now().Add(30 * 24 * time.Hour).Format(time.RFC3339),
	}
	pack := map[string]any{"items": []map[string]any{{"description": "battery", "quantity": 1, "weight_kg": 1000}}}
	resp, body = doJSON(t, srv, "POST", "/api/v1/bookings/"+bookingID+"/documents", map[string]any{
		"forwarder_id":   "FWD-1",
		"dg_certificate": cert,
		"packing_list":   pack,
	})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("submit docs status = %d, body = %v", resp.StatusCode, body)
	}
	if body["state"] != "docs_submitted" {
		t.Fatalf("state = %v, want docs_submitted", body["state"])
	}

	// Verify storage.
	resp, body = doJSON(t, srv, "POST", "/api/v1/bookings/"+bookingID+"/storage", map[string]any{"storage_location_id": storageID})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("verify storage status = %d, body = %v", resp.StatusCode, body)
	}
	if body["state"] != "storage_verified" {
		t.Fatalf("state = %v, want storage_verified", body["state"])
	}

	// Allocate space.
	resp, body = doJSON(t, srv, "POST", "/api/v1/bookings/"+bookingID+"/space", nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("allocate space status = %d, body = %v", resp.StatusCode, body)
	}
	if body["state"] != "space_allocated" {
		t.Fatalf("state = %v, want space_allocated", body["state"])
	}

	// Pay deposit.
	resp, body = doJSON(t, srv, "POST", "/api/v1/bookings/"+bookingID+"/deposit", map[string]any{"amount": 5000, "idempotency_key": "DEP-1"})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("pay deposit status = %d, body = %v", resp.StatusCode, body)
	}

	// Bind bill.
	resp, body = doJSON(t, srv, "POST", "/api/v1/bookings/"+bookingID+"/bind-bill", map[string]any{"bill_of_lading_id": "BL-1", "consignee_id": "CONS-1"})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("bind bill status = %d, body = %v", resp.StatusCode, body)
	}

	// Customs release.
	resp, body = doJSON(t, srv, "POST", "/api/v1/bookings/"+bookingID+"/customs/release", nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("customs release status = %d, body = %v", resp.StatusCode, body)
	}

	// Confirm.
	resp, body = doJSON(t, srv, "POST", "/api/v1/bookings/"+bookingID+"/confirm", nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("confirm status = %d, body = %v", resp.StatusCode, body)
	}
	if body["state"] != "confirmed" {
		t.Fatalf("state = %v, want confirmed", body["state"])
	}
}

func TestHTTPCreateBookingUnknownVoyage(t *testing.T) {
	srv, _ := newTestServer(t)
	resp, body := doJSON(t, srv, "POST", "/api/v1/bookings", map[string]any{
		"cargo_owner_id": "OWNER-1",
		"voyage_id":      "NOPE",
		"container_id":   "CNTR-1",
		"cargo_type":     "battery",
		"destination":    "Rotterdam",
	})
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("status = %d, want 404, body = %v", resp.StatusCode, body)
	}
}

func TestHTTPHealth(t *testing.T) {
	srv, _ := newTestServer(t)
	resp, body := doJSON(t, srv, "GET", "/api/v1/health", nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d", resp.StatusCode)
	}
	if body["status"] != "ok" {
		t.Fatalf("status = %v", body["status"])
	}
}

func TestHTTPVoyageCapacity(t *testing.T) {
	srv, _ := newTestServer(t)
	departure := time.Date(2026, 10, 1, 8, 0, 0, 0, time.UTC)
	cutoff := time.Date(2026, 9, 25, 8, 0, 0, 0, time.UTC)
	_, body := doJSON(t, srv, "POST", "/api/v1/voyages", map[string]any{
		"vessel_name":   "Arctic Sea",
		"voyage_number": "AE-002",
		"departure_at":  departure.Format(time.RFC3339),
		"cutoff_at":     cutoff.Format(time.RFC3339),
		"destinations":  []string{"Rotterdam"},
		"dg_capacity":   3,
		"gen_capacity":  5,
	})
	voyageID := body["id"].(string)

	resp, body := doJSON(t, srv, "GET", "/api/v1/voyages/"+voyageID+"/capacity", nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("capacity status = %d", resp.StatusCode)
	}
	if int(body["available_dg"].(float64)) != 3 {
		t.Fatalf("available DG = %v, want 3", body["available_dg"])
	}
	if int(body["available_gen"].(float64)) != 5 {
		t.Fatalf("available Gen = %v, want 5", body["available_gen"])
	}
}
