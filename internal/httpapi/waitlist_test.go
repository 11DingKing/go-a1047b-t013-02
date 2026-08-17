package httpapi

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

// seedVoyageHTTP creates a voyage with the given capacities and returns its id.
func seedVoyageHTTP(t *testing.T, srv *httptest.Server, number string, dgCap, genCap int) string {
	t.Helper()
	departure := time.Date(2026, 10, 1, 8, 0, 0, 0, time.UTC)
	cutoff := time.Date(2026, 9, 25, 8, 0, 0, 0, time.UTC)
	resp, body := doJSON(t, srv, "POST", "/api/v1/voyages", map[string]any{
		"vessel_name":   "Arctic Sea",
		"voyage_number": number,
		"departure_at":  departure.Format(time.RFC3339),
		"cutoff_at":     cutoff.Format(time.RFC3339),
		"destinations":  []string{"Rotterdam", "Hamburg", "Gdynia"},
		"dg_capacity":   dgCap,
		"gen_capacity":  genCap,
	})
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("create voyage: status %d body %v", resp.StatusCode, body)
	}
	return body["id"].(string)
}

// seedStorageHTTP creates a DG storage location and returns its id.
func seedStorageHTTP(t *testing.T, srv *httptest.Server, zone string, capacity int) string {
	t.Helper()
	resp, body := doJSON(t, srv, "POST", "/api/v1/storage-locations", map[string]any{
		"zone": zone, "capacity": capacity,
	})
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("create storage: status %d body %v", resp.StatusCode, body)
	}
	return body["id"].(string)
}

// dgBookingToStorageVerified walks a battery booking to storage_verified.
func dgBookingToStorageVerified(t *testing.T, srv *httptest.Server, voyageID, storageID, container string) string {
	t.Helper()
	resp, body := doJSON(t, srv, "POST", "/api/v1/bookings", map[string]any{
		"idempotency_key": container,
		"cargo_owner_id":  "OWNER-" + container,
		"voyage_id":       voyageID,
		"container_id":    container,
		"cargo_type":      "battery",
		"description":     "power battery",
		"weight_kg":       1000,
		"un_number":       "UN3480",
		"destination":     "Rotterdam",
	})
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("create booking %s: status %d body %v", container, resp.StatusCode, body)
	}
	id := body["id"].(string)

	cert := map[string]any{
		"id":            "DGC-" + container,
		"un_number":     "UN3480",
		"hazard_class":  "9",
		"packing_group": "II",
		"issued_at":     time.Date(2026, 8, 1, 8, 0, 0, 0, time.UTC).Format(time.RFC3339),
		"expires_at":    time.Date(2027, 8, 1, 8, 0, 0, 0, time.UTC).Format(time.RFC3339),
	}
	pack := map[string]any{"items": []map[string]any{{"description": "battery", "quantity": 1, "weight_kg": 1000}}}
	resp, body = doJSON(t, srv, "POST", "/api/v1/bookings/"+id+"/documents", map[string]any{
		"forwarder_id":   "FWD-1",
		"dg_certificate": cert,
		"packing_list":   pack,
	})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("submit docs %s: status %d body %v", container, resp.StatusCode, body)
	}
	resp, body = doJSON(t, srv, "POST", "/api/v1/bookings/"+id+"/storage", map[string]any{
		"storage_location_id": storageID,
	})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("verify storage %s: status %d body %v", container, resp.StatusCode, body)
	}
	return id
}

// TestHTTPSpaceRequestOnFullVoyageJoinsWaitlist asserts the space endpoint
// answers with a waitlisted booking instead of an error when the voyage is full.
func TestHTTPSpaceRequestOnFullVoyageJoinsWaitlist(t *testing.T) {
	srv, _ := newTestServer(t)
	voyageID := seedVoyageHTTP(t, srv, "AE-100", 1, 1)
	storageID := seedStorageHTTP(t, srv, "DG-A", 4)

	// First booking takes and confirms the single DG slot.
	first := dgBookingToStorageVerified(t, srv, voyageID, storageID, "CNTR-A")
	if resp, body := doJSON(t, srv, "POST", "/api/v1/bookings/"+first+"/space", nil); resp.StatusCode != http.StatusOK {
		t.Fatalf("allocate first: status %d body %v", resp.StatusCode, body)
	}
	if resp, body := doJSON(t, srv, "POST", "/api/v1/bookings/"+first+"/deposit", map[string]any{
		"amount": 5000, "idempotency_key": "DEP-A",
	}); resp.StatusCode != http.StatusOK {
		t.Fatalf("pay deposit first: status %d body %v", resp.StatusCode, body)
	}

	// Second booking arrives after capacity is fully taken.
	second := dgBookingToStorageVerified(t, srv, voyageID, storageID, "CNTR-B")
	resp, body := doJSON(t, srv, "POST", "/api/v1/bookings/"+second+"/space", nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("allocate second: status %d, want 200; body %v", resp.StatusCode, body)
	}
	if body["state"] != "waitlisted" {
		t.Fatalf("second booking state = %v, want waitlisted", body["state"])
	}

	resp, body = doJSON(t, srv, "GET", "/api/v1/voyages/"+voyageID+"/capacity", nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("capacity: status %d", resp.StatusCode)
	}
	if int(body["waitlist_length"].(float64)) != 1 {
		t.Fatalf("waitlist_length = %v, want 1", body["waitlist_length"])
	}
}

// TestHTTPWaitlistedBookingPromotedAfterCancel asserts the queued booking gets
// the slot when the confirmed booking is cancelled.
func TestHTTPWaitlistedBookingPromotedAfterCancel(t *testing.T) {
	srv, _ := newTestServer(t)
	voyageID := seedVoyageHTTP(t, srv, "AE-101", 1, 1)
	storageID := seedStorageHTTP(t, srv, "DG-A", 4)

	first := dgBookingToStorageVerified(t, srv, voyageID, storageID, "CNTR-A")
	doJSON(t, srv, "POST", "/api/v1/bookings/"+first+"/space", nil)
	doJSON(t, srv, "POST", "/api/v1/bookings/"+first+"/deposit", map[string]any{
		"amount": 5000, "idempotency_key": "DEP-A",
	})

	second := dgBookingToStorageVerified(t, srv, voyageID, storageID, "CNTR-B")
	if resp, body := doJSON(t, srv, "POST", "/api/v1/bookings/"+second+"/space", nil); resp.StatusCode != http.StatusOK {
		t.Fatalf("allocate second: status %d body %v", resp.StatusCode, body)
	}

	if resp, body := doJSON(t, srv, "POST", "/api/v1/bookings/"+first+"/cancel", map[string]any{
		"reason": "owner cancelled",
	}); resp.StatusCode != http.StatusOK {
		t.Fatalf("cancel first: status %d body %v", resp.StatusCode, body)
	}

	resp, body := doJSON(t, srv, "GET", "/api/v1/bookings/"+second, nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("get second: status %d", resp.StatusCode)
	}
	if body["state"] != "space_allocated" {
		t.Fatalf("second booking state = %v, want space_allocated", body["state"])
	}
}

// TestHTTPStorageFullStillReportsConflict asserts the DG storage side of the
// no-capacity rule keeps returning 409.
func TestHTTPStorageFullStillReportsConflict(t *testing.T) {
	srv, _ := newTestServer(t)
	voyageID := seedVoyageHTTP(t, srv, "AE-102", 4, 4)
	storageID := seedStorageHTTP(t, srv, "DG-A", 1)

	dgBookingToStorageVerified(t, srv, voyageID, storageID, "CNTR-A")

	resp, body := doJSON(t, srv, "POST", "/api/v1/bookings", map[string]any{
		"idempotency_key": "CNTR-B",
		"cargo_owner_id":  "OWNER-B",
		"voyage_id":       voyageID,
		"container_id":    "CNTR-B",
		"cargo_type":      "battery",
		"description":     "power battery",
		"weight_kg":       1000,
		"un_number":       "UN3480",
		"destination":     "Rotterdam",
	})
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("create second: status %d body %v", resp.StatusCode, body)
	}
	second := body["id"].(string)
	cert := map[string]any{
		"id":         "DGC-B",
		"un_number":  "UN3480",
		"issued_at":  time.Date(2026, 8, 1, 8, 0, 0, 0, time.UTC).Format(time.RFC3339),
		"expires_at": time.Date(2027, 8, 1, 8, 0, 0, 0, time.UTC).Format(time.RFC3339),
	}
	pack := map[string]any{"items": []map[string]any{{"description": "battery", "quantity": 1, "weight_kg": 1000}}}
	doJSON(t, srv, "POST", "/api/v1/bookings/"+second+"/documents", map[string]any{
		"forwarder_id":   "FWD-1",
		"dg_certificate": cert,
		"packing_list":   pack,
	})
	resp, body = doJSON(t, srv, "POST", "/api/v1/bookings/"+second+"/storage", map[string]any{
		"storage_location_id": storageID,
	})
	if resp.StatusCode != http.StatusConflict {
		t.Fatalf("storage on a full location: status %d, want 409; body %v", resp.StatusCode, body)
	}
}
