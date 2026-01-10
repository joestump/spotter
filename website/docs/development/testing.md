---
sidebar_position: 3
---

# Testing

Guide to testing Spotter during development.

## Running Tests

### All Tests

```bash
make test
```

### With Coverage

```bash
make test-coverage
```

This generates:
- `coverage.out` - Coverage data
- `coverage.html` - HTML report

### Specific Package

```bash
go test ./internal/providers/spotify/...
```

### Verbose Output

```bash
go test -v ./...
```

## Test Structure

Tests are located alongside the code they test:

```
internal/
├── providers/
│   └── spotify/
│       ├── spotify.go
│       └── spotify_test.go
```

## Writing Tests

### Unit Tests

```go
func TestSomething(t *testing.T) {
    // Arrange
    input := "test"

    // Act
    result := DoSomething(input)

    // Assert
    if result != expected {
        t.Errorf("got %v, want %v", result, expected)
    }
}
```

### Table-Driven Tests

```go
func TestSomething(t *testing.T) {
    tests := []struct {
        name     string
        input    string
        expected string
    }{
        {"empty", "", ""},
        {"simple", "test", "TEST"},
    }

    for _, tt := range tests {
        t.Run(tt.name, func(t *testing.T) {
            result := DoSomething(tt.input)
            if result != tt.expected {
                t.Errorf("got %v, want %v", result, tt.expected)
            }
        })
    }
}
```

### Mocking

Use interfaces for dependencies to enable mocking:

```go
type SpotifyClient interface {
    GetRecentlyPlayed(ctx context.Context) ([]Track, error)
}

// In tests
type mockSpotifyClient struct {
    tracks []Track
    err    error
}

func (m *mockSpotifyClient) GetRecentlyPlayed(ctx context.Context) ([]Track, error) {
    return m.tracks, m.err
}
```

## Integration Tests

### Database Tests

```go
func TestDatabaseIntegration(t *testing.T) {
    if testing.Short() {
        t.Skip("skipping integration test")
    }

    // Use test database
    client := setupTestDB(t)
    defer client.Close()

    // Run tests
}
```

### API Tests

```go
func TestAPIEndpoint(t *testing.T) {
    srv := httptest.NewServer(handler)
    defer srv.Close()

    resp, err := http.Get(srv.URL + "/api/listens")
    if err != nil {
        t.Fatal(err)
    }

    if resp.StatusCode != http.StatusOK {
        t.Errorf("got status %d", resp.StatusCode)
    }
}
```

## Test Utilities

### Test Fixtures

Place test data in `testdata/` directories:

```
internal/
├── enrichers/
│   └── spotify/
│       ├── testdata/
│       │   └── track.json
│       └── spotify_test.go
```

### Helper Functions

```go
func setupTestDB(t *testing.T) *ent.Client {
    t.Helper()

    client, err := ent.Open("sqlite3", ":memory:")
    if err != nil {
        t.Fatal(err)
    }

    if err := client.Schema.Create(context.Background()); err != nil {
        t.Fatal(err)
    }

    return client
}
```

## Continuous Integration

Tests run automatically on:
- Pull requests
- Pushes to main

See `.github/workflows/` for CI configuration.

## Linting

Run all linters:

```bash
make lint
```

Individual linters:
- `make lint-go` - Go code
- `make lint-templ` - Templates
- `make lint-md` - Markdown
- `make lint-yaml` - YAML files
- `make lint-docker` - Dockerfile
