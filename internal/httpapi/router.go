package httpapi

import "net/http"

// Router builds the HTTP mux with all Arctic Express endpoints.
func Router(h *Handler) http.Handler {
	mux := http.NewServeMux()

	// Health
	mux.HandleFunc("GET /api/v1/health", h.health)

	// Booking lifecycle
	mux.HandleFunc("POST /api/v1/bookings", h.createBooking)
	mux.HandleFunc("GET /api/v1/bookings", h.listBookings)
	mux.HandleFunc("GET /api/v1/bookings/{id}", h.getBooking)
	mux.HandleFunc("POST /api/v1/bookings/{id}/documents", h.submitDocuments)
	mux.HandleFunc("POST /api/v1/bookings/{id}/storage", h.verifyStorage)
	mux.HandleFunc("POST /api/v1/bookings/{id}/space", h.allocateSpace)
	mux.HandleFunc("POST /api/v1/bookings/{id}/deposit", h.payDeposit)
	mux.HandleFunc("POST /api/v1/bookings/{id}/customs/release", h.customsRelease)
	mux.HandleFunc("POST /api/v1/bookings/{id}/customs/reject", h.customsReject)
	mux.HandleFunc("POST /api/v1/bookings/{id}/confirm", h.confirmBooking)
	mux.HandleFunc("POST /api/v1/bookings/{id}/cancel", h.cancelBooking)
	mux.HandleFunc("POST /api/v1/bookings/{id}/change-port", h.changePort)
	mux.HandleFunc("POST /api/v1/bookings/{id}/bind-bill", h.bindBill)
	mux.HandleFunc("POST /api/v1/bookings/{id}/requeue", h.requeueBooking)

	// Voyage management
	mux.HandleFunc("POST /api/v1/voyages", h.createVoyage)
	mux.HandleFunc("GET /api/v1/voyages", h.listVoyages)
	mux.HandleFunc("GET /api/v1/voyages/{id}", h.getVoyage)
	mux.HandleFunc("GET /api/v1/voyages/{id}/capacity", h.voyageCapacity)

	// Storage location management
	mux.HandleFunc("POST /api/v1/storage-locations", h.createStorageLocation)
	mux.HandleFunc("GET /api/v1/storage-locations", h.listStorageLocations)

	// Payment queries
	mux.HandleFunc("GET /api/v1/bookings/{id}/deposit", h.getDeposit)

	return mux
}
