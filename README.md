# Arctic Express — Booking Orchestration Backend

A Go backend for **Haijie Shipping Arctic Express**, coordinating Chuanshan-port
container space and dangerous-goods storage for the 20-day direct Europe service.
It serves cargo owners, freight forwarders, terminal operators, shipping
companies and customs for booking the Ningbo/Yiwu → Rotterdam / Hamburg / Gdynia
route.

## Business rules implemented

- Battery / dangerous cargo must upload a DG certificate ("危包证") and lock a
  storage slot 48 h before the customs cutoff.
- A tentative space hold is released automatically if no deposit arrives within
  the hold deadline (default 2 h).
- One container binds exactly one bill of lading and one consignee.
- Dangerous and general cargo never share a hold (separate capacity pools).
- Destination-port changes require written confirmation 72 h before departure.
- Concurrent deposits for the same remaining slot are resolved first-deposit
  first-served; losers move to the waitlist and are offered the next voyage.
- On cancellation or customs rejection, the system rolls back voyage space and
  storage, refunds the deposit via the original path, and **retains** the DG
  certificate and packing list so a specialist can re-queue the booking without
  duplicate declarations.

## Architecture

| Package | Responsibility |
|---|---|
| `internal/domain` | Booking, Voyage, Cargo, Storage, Deposit aggregates and state machine |
| `internal/store` | Concurrency-safe, snapshot-backed in-memory repository with transactional clone-on-write |
| `internal/booking` | Booking lifecycle orchestration, rollback, waitlist promotion |
| `internal/voyage` | Voyage CRUD and capacity reporting |
| `internal/storage` | Dangerous-goods storage location CRUD |
| `internal/payment` | Deposit queries and standalone refunds |
| `internal/scheduler` | Background hold-timeout and cutoff-compliance checks |
| `internal/httpapi` | HTTP handlers and routing |

## Running

```bash
go run ./cmd/server            # uses config.json, listens on :58594
```

## Configuration

`config.json` (overridable via `CONFIG_PATH` env var):

| Key | Default | Description |
|---|---|---|
| `port` | `58594` | HTTP listen port |
| `store_path` | `data/store.json` | Snapshot file path |
| `hold_duration` | `2h` | Deposit deadline for tentative holds |
| `cutoff_threshold` | `48h` | DG cert + storage must be ready before cutoff |
| `port_change_window` | `72h` | Latest window for port changes before departure |
| `scheduler_interval` | `30s` | Background check cadence |

## Main API endpoints

```
POST   /api/v1/bookings                       create booking (cargo owner)
GET    /api/v1/bookings                       list bookings
GET    /api/v1/bookings/{id}                  get booking detail
POST   /api/v1/bookings/{id}/documents        submit DG cert + packing list (forwarder)
POST   /api/v1/bookings/{id}/storage          verify/reserve DG storage (terminal)
POST   /api/v1/bookings/{id}/space            allocate voyage space (shipping co.)
POST   /api/v1/bookings/{id}/deposit          pay deposit
POST   /api/v1/bookings/{id}/customs/release  customs release
POST   /api/v1/bookings/{id}/customs/reject   customs reject → rollback + review
POST   /api/v1/bookings/{id}/confirm          confirm booking
POST   /api/v1/bookings/{id}/cancel            cancel booking
POST   /api/v1/bookings/{id}/change-port      change destination port
POST   /api/v1/bookings/{id}/bind-bill        bind bill of lading + consignee
POST   /api/v1/bookings/{id}/requeue          specialist re-queue from review
POST   /api/v1/voyages                        create voyage
GET    /api/v1/voyages                        list voyages
GET    /api/v1/voyages/{id}/capacity          voyage capacity report
POST   /api/v1/storage-locations              create storage location
GET    /api/v1/storage-locations              list storage locations
GET    /api/v1/bookings/{id}/deposit          deposit detail
GET    /api/v1/health                         health check
```

## Testing

```bash
go test -timeout=120s -count=1 ./...
```

## Docker

```bash
docker build -t arcticexpress .
docker run -p 58594:58594 arcticexpress
```

The Dockerfile uses a two-stage build (`golang:1.26-alpine` → `alpine:3.21`),
produces a CGO-free static binary, and supports `--platform linux/amd64` and
`linux/arm64` builds.
