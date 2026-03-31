# Contributing to hush-server

Thank you for your interest in contributing to the Hush server. This guide covers the development setup, testing approach, code style, and pull request process.

---

## Prerequisites

- [Go](https://golang.org/dl/) 1.25 or later
- [PostgreSQL](https://www.postgresql.org/) 16 or later
- [Redis](https://redis.io/) 7 or later
- [Docker](https://docs.docker.com/engine/install/) and docker-compose (optional but recommended for running dependencies)

---

## Development Setup

**1. Clone the repository:**

```bash
git clone https://github.com/hushhq/hush-server
cd hush-server
```

**2. Start dependencies:**

```bash
docker-compose up -d postgres redis livekit
```

**3. Copy and configure environment variables:**

```bash
cp .env.example .env
# Edit .env — at minimum fill in DATABASE_URL and JWT_SECRET
```

**4. Run database migrations:**

```bash
go run github.com/golang-migrate/migrate/v4/cmd/migrate@latest \
  -database "$DATABASE_URL" \
  -path ./migrations up
```

**5. Start the server:**

```bash
cd server
go run ./cmd/hush
```

The API listens on `http://localhost:8080` by default.

---

## Running Tests

```bash
cd server

# Run all tests
go test ./...

# Run with race detector (required before opening a PR)
go test -race ./...

# Run a specific package
go test ./internal/api/...
go test ./internal/ws/...

# With verbose output
go test -v ./...
```

All tests must pass with `-race` before submitting a pull request.

---

## Code Style

This project follows standard Go conventions plus the guidelines in the root `CLAUDE.md`:

- **Function length:** Keep functions under 30 lines where possible. Extract helpers if a function grows.
- **Single responsibility:** Each handler, DB function, and helper does one thing.
- **Error handling:** Never swallow errors silently. Log or propagate.
- **Naming:** Exported types and functions use PascalCase. Unexported use camelCase. Variables reveal intent — no cryptic abbreviations.
- **Dependency injection:** Handlers receive a `Store` interface, not a concrete DB type. This enables clean testing with mocks.
- **No magic numbers:** Define named constants for permission levels, rate limits, and similar values.

Run `gofmt` and `go vet` before committing:

```bash
gofmt -w ./...
go vet ./...
```

---

## Pull Request Process

1. **Open an issue first** for non-trivial changes to discuss the approach before writing code.
2. **Branch from `main`:** `git checkout -b feature/my-feature` or `fix/my-fix`.
3. **Write tests** for new functionality. Bug fixes should include a regression test.
4. **Run tests with `-race`** and confirm they pass.
5. **Keep commits focused.** One logical change per commit with a clear message.
6. **Open a pull request** against `main` with a description of what changed and why.
7. A maintainer will review and respond within a few days.

### Commit message format

```
type(scope): short description

- change detail 1
- change detail 2
```

Types: `feat`, `fix`, `test`, `refactor`, `chore`, `docs`.

---

## Security Issues

Do not open public issues for security vulnerabilities. See [SECURITY.md](SECURITY.md) for the responsible disclosure process.
