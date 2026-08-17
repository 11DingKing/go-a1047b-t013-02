package httpapi

import (
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"time"

	"arcticexpress/internal/booking"
	"arcticexpress/internal/domain"
	"arcticexpress/internal/payment"
	"arcticexpress/internal/storage"
	"arcticexpress/internal/voyage"
)

// Handler wires service calls into HTTP endpoints.
type Handler struct {
	booking *booking.Service
	voyage  *voyage.Service
	storage *storage.Service
	payment *payment.Service
}

// NewHandler constructs a handler from the application services.
func NewHandler(bs *booking.Service, vs *voyage.Service, ss *storage.Service, ps *payment.Service) *Handler {
	return &Handler{booking: bs, voyage: vs, storage: ss, payment: ps}
}

// --- DTOs ---

type createBookingReq struct {
	IdempotencyKey string  `json:"idempotency_key"`
	CargoOwnerID   string  `json:"cargo_owner_id"`
	VoyageID       string  `json:"voyage_id"`
	ContainerID    string  `json:"container_id"`
	CargoType      string  `json:"cargo_type"`
	Description    string  `json:"description"`
	WeightKg       float64 `json:"weight_kg"`
	UNNumber       string  `json:"un_number"`
	Destination    string  `json:"destination"`
}

type submitDocsReq struct {
	ForwarderID string                            `json:"forwarder_id"`
	DGCert      *domain.DangerousGoodsCertificate `json:"dg_certificate,omitempty"`
	PackingList *domain.PackingList               `json:"packing_list,omitempty"`
}

type verifyStorageReq struct {
	StorageLocationID string `json:"storage_location_id"`
}

type payDepositReq struct {
	Amount         float64 `json:"amount"`
	IdempotencyKey string  `json:"idempotency_key"`
}

type bindBillReq struct {
	BillOfLadingID string `json:"bill_of_lading_id"`
	ConsigneeID    string `json:"consignee_id"`
}

type changePortReq struct {
	NewPort string `json:"new_port"`
}

type rejectReq struct {
	Reason string `json:"reason"`
}

type cancelReq struct {
	Reason string `json:"reason"`
}

type createVoyageReq struct {
	VesselName   string   `json:"vessel_name"`
	VoyageNumber string   `json:"voyage_number"`
	DepartureAt  string   `json:"departure_at"`
	CutoffAt     string   `json:"cutoff_at"`
	Destinations []string `json:"destinations"`
	DGCapacity   int      `json:"dg_capacity"`
	GenCapacity  int      `json:"gen_capacity"`
}

type createStorageReq struct {
	Zone     string `json:"zone"`
	Capacity int    `json:"capacity"`
}

type bookingResp struct {
	ID                string    `json:"id"`
	State             string    `json:"state"`
	CargoOwnerID      string    `json:"cargo_owner_id"`
	ForwarderID       string    `json:"forwarder_id"`
	VoyageID          string    `json:"voyage_id"`
	ContainerID       string    `json:"container_id"`
	CargoType         string    `json:"cargo_type"`
	Destination       string    `json:"destination"`
	HasDGCertificate  bool      `json:"has_dg_certificate"`
	HasPackingList    bool      `json:"has_packing_list"`
	StorageLocationID string    `json:"storage_location_id"`
	StorageLocked     bool      `json:"storage_locked"`
	BillOfLadingID    string    `json:"bill_of_lading_id"`
	ConsigneeID       string    `json:"consignee_id"`
	RequeueCount      int       `json:"requeue_count"`
	CancelReason      string    `json:"cancel_reason"`
	CreatedAt         time.Time `json:"created_at"`
	UpdatedAt         time.Time `json:"updated_at"`
}

func toBookingResp(b *domain.Booking) bookingResp {
	return bookingResp{
		ID:                b.ID,
		State:             string(b.State),
		CargoOwnerID:      b.CargoOwnerID,
		ForwarderID:       b.ForwarderID,
		VoyageID:          b.VoyageID,
		ContainerID:       b.ContainerID,
		CargoType:         string(b.Cargo.Type),
		Destination:       string(b.Destination),
		HasDGCertificate:  b.HasDGCertificate(),
		HasPackingList:    b.HasPackingList(),
		StorageLocationID: b.StorageLocationID,
		StorageLocked:     b.StorageLocked,
		BillOfLadingID:    b.BillOfLadingID,
		ConsigneeID:       b.ConsigneeID,
		RequeueCount:      b.RequeueCount,
		CancelReason:      b.CancelReason,
		CreatedAt:         b.CreatedAt,
		UpdatedAt:         b.UpdatedAt,
	}
}

// --- Helpers ---

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if v != nil {
		_ = json.NewEncoder(w).Encode(v)
	}
}

type errBody struct {
	Error string `json:"error"`
}

func writeErr(w http.ResponseWriter, err error) {
	status := http.StatusBadRequest
	switch {
	case errors.Is(err, domain.ErrNotFound):
		status = http.StatusNotFound
	case errors.Is(err, domain.ErrConflict):
		status = http.StatusConflict
	case errors.Is(err, domain.ErrInvalidState), errors.Is(err, domain.ErrWindowClosed):
		status = http.StatusUnprocessableEntity
	case errors.Is(err, domain.ErrMissingDocs):
		status = http.StatusUnprocessableEntity
	case errors.Is(err, domain.ErrNoCapacity), errors.Is(err, domain.ErrAlreadyAllocated),
		errors.Is(err, domain.ErrAlreadyConfirmed), errors.Is(err, domain.ErrAlreadyReserved):
		status = http.StatusConflict
	}
	slog.Debug("request error", "status", status, "error", err)
	writeJSON(w, status, errBody{Error: err.Error()})
}

func decode(r *http.Request, v any) error {
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	return dec.Decode(v)
}

// --- Booking endpoints ---

func (h *Handler) createBooking(w http.ResponseWriter, r *http.Request) {
	var req createBookingReq
	if err := decode(r, &req); err != nil {
		writeErr(w, err)
		return
	}
	b, err := h.booking.Create(booking.CreateBookingRequest{
		IdempotencyKey: req.IdempotencyKey,
		CargoOwnerID:   req.CargoOwnerID,
		VoyageID:       req.VoyageID,
		ContainerID:    req.ContainerID,
		Cargo: domain.Cargo{
			Description: req.Description,
			Type:        domain.CargoType(req.CargoType),
			WeightKg:    req.WeightKg,
			UNNumber:    req.UNNumber,
			Destination: domain.DestinationPort(req.Destination),
		},
	})
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, toBookingResp(b))
}

func (h *Handler) listBookings(w http.ResponseWriter, r *http.Request) {
	bookings, err := h.booking.List()
	if err != nil {
		writeErr(w, err)
		return
	}
	resp := make([]bookingResp, 0, len(bookings))
	for _, b := range bookings {
		resp = append(resp, toBookingResp(b))
	}
	writeJSON(w, http.StatusOK, resp)
}

func (h *Handler) getBooking(w http.ResponseWriter, r *http.Request) {
	b, err := h.booking.Get(r.PathValue("id"))
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, toBookingResp(b))
}

func (h *Handler) submitDocuments(w http.ResponseWriter, r *http.Request) {
	var req submitDocsReq
	if err := decode(r, &req); err != nil {
		writeErr(w, err)
		return
	}
	b, err := h.booking.SubmitDocuments(booking.SubmitDocumentsRequest{
		BookingID:   r.PathValue("id"),
		ForwarderID: req.ForwarderID,
		DGCert:      req.DGCert,
		PackingList: req.PackingList,
	})
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, toBookingResp(b))
}

func (h *Handler) verifyStorage(w http.ResponseWriter, r *http.Request) {
	var req verifyStorageReq
	if err := decode(r, &req); err != nil {
		writeErr(w, err)
		return
	}
	b, err := h.booking.VerifyStorage(r.PathValue("id"), req.StorageLocationID)
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, toBookingResp(b))
}

func (h *Handler) allocateSpace(w http.ResponseWriter, r *http.Request) {
	b, err := h.booking.AllocateSpace(r.PathValue("id"))
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, toBookingResp(b))
}

func (h *Handler) payDeposit(w http.ResponseWriter, r *http.Request) {
	var req payDepositReq
	if err := decode(r, &req); err != nil {
		writeErr(w, err)
		return
	}
	b, dep, err := h.booking.PayDeposit(r.PathValue("id"), req.Amount, req.IdempotencyKey)
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"booking": toBookingResp(b), "deposit": dep})
}

func (h *Handler) customsRelease(w http.ResponseWriter, r *http.Request) {
	b, err := h.booking.CustomsRelease(r.PathValue("id"))
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, toBookingResp(b))
}

func (h *Handler) customsReject(w http.ResponseWriter, r *http.Request) {
	var req rejectReq
	if err := decode(r, &req); err != nil {
		writeErr(w, err)
		return
	}
	b, err := h.booking.CustomsReject(r.PathValue("id"), req.Reason)
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, toBookingResp(b))
}

func (h *Handler) confirmBooking(w http.ResponseWriter, r *http.Request) {
	b, err := h.booking.Confirm(r.PathValue("id"))
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, toBookingResp(b))
}

func (h *Handler) cancelBooking(w http.ResponseWriter, r *http.Request) {
	var req cancelReq
	if err := decode(r, &req); err != nil {
		writeErr(w, err)
		return
	}
	b, err := h.booking.Cancel(r.PathValue("id"), req.Reason)
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, toBookingResp(b))
}

func (h *Handler) changePort(w http.ResponseWriter, r *http.Request) {
	var req changePortReq
	if err := decode(r, &req); err != nil {
		writeErr(w, err)
		return
	}
	b, err := h.booking.ChangePort(r.PathValue("id"), domain.DestinationPort(req.NewPort))
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, toBookingResp(b))
}

func (h *Handler) bindBill(w http.ResponseWriter, r *http.Request) {
	var req bindBillReq
	if err := decode(r, &req); err != nil {
		writeErr(w, err)
		return
	}
	b, err := h.booking.BindBill(r.PathValue("id"), req.BillOfLadingID, req.ConsigneeID)
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, toBookingResp(b))
}

func (h *Handler) requeueBooking(w http.ResponseWriter, r *http.Request) {
	b, err := h.booking.Requeue(r.PathValue("id"))
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, toBookingResp(b))
}

// --- Voyage endpoints ---

func (h *Handler) createVoyage(w http.ResponseWriter, r *http.Request) {
	var req createVoyageReq
	if err := decode(r, &req); err != nil {
		writeErr(w, err)
		return
	}
	departure, err := time.Parse(time.RFC3339, req.DepartureAt)
	if err != nil {
		writeErr(w, err)
		return
	}
	cutoff, err := time.Parse(time.RFC3339, req.CutoffAt)
	if err != nil {
		writeErr(w, err)
		return
	}
	dests := make([]domain.DestinationPort, len(req.Destinations))
	for i, d := range req.Destinations {
		dests[i] = domain.DestinationPort(d)
	}
	v, err := h.voyage.Create(voyage.CreateVoyageRequest{
		VesselName:   req.VesselName,
		VoyageNumber: req.VoyageNumber,
		DepartureAt:  departure,
		CutoffAt:     cutoff,
		Destinations: dests,
		DGCapacity:   req.DGCapacity,
		GenCapacity:  req.GenCapacity,
	})
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, v)
}

func (h *Handler) listVoyages(w http.ResponseWriter, r *http.Request) {
	voyages, err := h.voyage.List()
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, voyages)
}

func (h *Handler) getVoyage(w http.ResponseWriter, r *http.Request) {
	v, err := h.voyage.Get(r.PathValue("id"))
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, v)
}

func (h *Handler) voyageCapacity(w http.ResponseWriter, r *http.Request) {
	report, err := h.voyage.Capacity(r.PathValue("id"))
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, report)
}

// --- Storage endpoints ---

func (h *Handler) createStorageLocation(w http.ResponseWriter, r *http.Request) {
	var req createStorageReq
	if err := decode(r, &req); err != nil {
		writeErr(w, err)
		return
	}
	sl, err := h.storage.Create(storage.CreateRequest{Zone: req.Zone, Capacity: req.Capacity})
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, sl)
}

func (h *Handler) listStorageLocations(w http.ResponseWriter, r *http.Request) {
	locations, err := h.storage.List()
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, locations)
}

// --- Payment endpoints ---

func (h *Handler) getDeposit(w http.ResponseWriter, r *http.Request) {
	dep, err := h.payment.GetByBooking(r.PathValue("id"))
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, dep)
}

func (h *Handler) health(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}
