# Quickstart Validation Guide: SA-02 L0 Database Foundation

## 1. Offline Unit Verification (Zero External Dependencies)

```bash
cd synapse-server
gofmt -l .
go vet ./...
go build ./...
go test -race -v ./internal/infra/db/...
go test -race ./...
```

Expected: All offline unit tests pass; PG17 integration tests auto-skip cleanly when `TEST_POSTGRES_DSN` is unset; all `SA-01` tests remain green.

## 2. Live PostgreSQL 17 Integration Verification

```bash
# Start ephemeral PG17 container (avoiding host port 5432 collisions if needed)
POSTGRES_PORT=15432 docker compose -f docker-compose.dev.yml up -d --wait

cd synapse-server
TEST_POSTGRES_DSN="postgres://synapse:synapse@127.0.0.1:15432/synapse?sslmode=disable&TimeZone=UTC" \
  go test -race -v -cover ./internal/infra/db/...

# Tear down ephemeral PG17 container after tests
cd ..
docker compose -f docker-compose.dev.yml down -v
```

Expected:
- All 9 test scenarios in `spec_02 §8` execute and pass under `-race` with zero skipped tests.
- Statement coverage for `internal/infra/db` is **≥ 80%** (per `spec_02 §9`).
- Temporary `test_*` schemas are completely cleaned up after tests complete.
