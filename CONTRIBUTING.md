# Contributing to Manifold

Thank you for your interest in contributing to Manifold! Contributions of all kinds are welcome: bug reports, feature requests, documentation improvements, and code changes.

日本語での Issue / Pull Request も歓迎します。

## Getting started

### Prerequisites

- Go 1.26+ (see [`.go-version`](.go-version))
- Docker / Docker Compose (for Redis and integration tests)
- [golangci-lint](https://golangci-lint.run/) (for linting)

### Setup

```bash
git clone https://github.com/nonchan7720/manifold.git
cd manifold

# Start dependencies (Redis, etc.)
docker compose up -d

# Run the gateway from source
go run main.go gateway
```

### Running tests

```bash
mkdir -p coverage
make test
```

### Linting

```bash
make lint
```

CI runs both lint and tests on every pull request, so running them locally first saves a round trip.

## How to contribute

### Reporting bugs

Open a [bug report](https://github.com/nonchan7720/manifold/issues/new?template=bug_report.yml) with:

- What you expected to happen and what actually happened
- Steps to reproduce (a minimal `config.yaml` helps a lot — **remove any secrets first**)
- Manifold version, OS, and how you run it (binary / Docker / source)

For security vulnerabilities, please do **not** open a public issue — see [SECURITY.md](SECURITY.md).

### Suggesting features

Open a [feature request](https://github.com/nonchan7720/manifold/issues/new?template=feature_request.yml). Describing the use case (what you are trying to achieve, not just the solution you have in mind) makes it much easier to discuss.

### Submitting pull requests

1. Fork the repository and create a branch from `main`.
2. Make your changes. Please add or update tests for behavior changes.
3. Run `make test` and `make lint` locally.
4. Open a pull request against `main`.

Notes:

- **PR titles must follow [Conventional Commits](https://www.conventionalcommits.org/)** (e.g. `feat: add healthz path`, `fix: handle empty spec URL`). This is enforced by CI and drives automated releases via release-please.
  - Common types: `feat`, `fix`, `docs`, `refactor`, `test`, `ci`, `chore`
- Keep pull requests focused — one logical change per PR is easier to review.
- For large changes or new features, consider opening an issue first to discuss the direction before investing significant effort.

## Release process

Releases are automated with [release-please](https://github.com/googleapis/release-please) and [GoReleaser](https://goreleaser.com/). Merged PRs with `feat:` / `fix:` titles are collected into a release PR; merging that release PR publishes a new version and updates the [CHANGELOG](CHANGELOG.md). You do not need to touch version numbers or the changelog manually.

## Code of Conduct

This project follows the [Code of Conduct](CODE_OF_CONDUCT.md). By participating, you are expected to uphold it.

## License

By contributing, you agree that your contributions will be licensed under the [MIT License](LICENSE).
