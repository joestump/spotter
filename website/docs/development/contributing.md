---
sidebar_position: 2
---

# Contributing

We welcome contributions to Spotter! This guide will help you get started.

## Development Setup

### Prerequisites

- Go 1.23+
- Node.js & npm
- Make
- Git

### Clone and Setup

```bash
git clone https://github.com/joestump/spotter.git
cd spotter
make deps
```

### Running Locally

```bash
make dev
```

This starts:
- Go server with hot-reload (Air)
- Template watching (templ)
- CSS watching (Tailwind)

## Code Style

### Go

- Follow standard Go conventions
- Use `gofmt` for formatting
- Run linters before committing:

```bash
make lint-go
```

### Templates

- Use templ for all HTML templates
- Keep templates in `internal/templates/`
- Format with:

```bash
make lint-templ
```

### CSS

- Use Tailwind utility classes
- Custom styles go in `static/css/input.css`
- DaisyUI for component patterns

### Markdown

- Follow markdownlint rules
- Run linter:

```bash
make lint-md
```

## Making Changes

### 1. Create a Branch

```bash
git checkout -b feature/your-feature-name
```

### 2. Make Your Changes

- Write clean, documented code
- Add tests for new functionality
- Update documentation if needed

### 3. Run Tests

```bash
make test
```

### 4. Run Linters

```bash
make lint
```

### 5. Commit

Follow conventional commit messages:

```
feat: add new feature
fix: resolve bug in playlist sync
docs: update API documentation
refactor: simplify enricher interface
```

### 6. Create Pull Request

- Push your branch
- Open a PR against `main`
- Fill out the PR template
- Wait for review

## Project Structure

```
internal/
├── config/        # Configuration
├── handlers/      # HTTP handlers
├── providers/     # External service providers
├── enrichers/     # Metadata enrichers
├── services/      # Business logic
├── templates/     # templ templates
└── middleware/    # HTTP middleware
```

## Adding a Provider

1. Create directory in `internal/providers/`
2. Implement the Provider interface
3. Register in the provider registry
4. Add configuration options
5. Write tests
6. Update documentation

## Adding an Enricher

1. Create directory in `internal/enrichers/`
2. Implement the Enricher interface
3. Register in the enricher registry
4. Add to default enricher order
5. Write tests
6. Update documentation

## Database Changes

1. Modify schema in `ent/schema/`
2. Generate ent code:

```bash
go generate ./ent
```

3. Migrations are automatic with ent

## Testing

### Unit Tests

```bash
go test ./...
```

### Integration Tests

```bash
go test -tags=integration ./...
```

### Coverage

```bash
make test-coverage
```

## Documentation

- Update relevant docs in `website/docs/`
- Run docs locally:

```bash
cd website && npm start
```

## Questions?

- Open a GitHub issue
- Start a discussion
- Check existing issues first
