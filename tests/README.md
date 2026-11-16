# Agent RPG 2024 Tests

## Directory Structure

```
tests/
├── api/                  # API endpoint tests
├── mechanics/            # Game rule tests  
├── integration/          # End-to-end flows
└── website/              # Frontend tests
```

## Running Tests

```bash
# All tests
go test ./...

# Specific category
go test ./tests/api/...

# With coverage
go test ./... -cover -coverprofile=coverage.out
go tool cover -html=coverage.out
```

## Database Setup

The server's tests against PostgreSQL (`cmd/server/postgres_integration_test.go`) run the server's own start (the schema and
the SRD data) on a throwaway database and drive requests through the whole server. They read only `TEST_DATABASE_URL`, never
`DATABASE_URL`, and refuse a database whose name doesn't contain "test":

```bash
createdb agentrpg_test
export TEST_DATABASE_URL="postgres://user:pass@localhost:5432/agentrpg_test?sslmode=disable"
go test ./cmd/server -run TestPostgres
```

Without `TEST_DATABASE_URL` they are skipped; with `REQUIRE_TEST_DATABASE=1` (CI) a missing database fails them instead.
`tests/api` checks a running server: set `AGENTRPG_URL` (for example `http://localhost:8080`), or they are skipped.

## CI

`.github/workflows/test.yml` runs on every push to `agentrpg2024`, `staging` and `claude/**` and on pull requests to
`agentrpg2024` and `staging`, with a Postgres service: `go vet ./cmd/server ./game`, `gofmt -l` on the Go files the push changed,
and `go test -race ./...` with `TEST_DATABASE_URL` and `REQUIRE_TEST_DATABASE=1`.
