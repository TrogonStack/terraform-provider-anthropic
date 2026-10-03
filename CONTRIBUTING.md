# Contributing

## Prerequisites

- [mise](https://mise.jdx.dev), which pins the Go, `golangci-lint`, GoReleaser, and `tfplugindocs` versions used by CI

Install the toolchain with `mise install`. Every command below runs through `mise` so local runs match CI.

## Development

```bash
mise run build   # full CI pipeline: download, lint, test, tidy, docs, diff
mise run test    # go test -count=1 -cover -skip TestLive ./...
mise run lint    # golangci-lint run --fix ./...
mise run docs    # regenerate docs/ from schema descriptions
```

`mise run build` is what CI runs on every pull request, including the `git diff --exit-code` check, so run it before pushing.

## Testing

`mise run test` never reaches the Anthropic API. Resource tests serve `fakeAdminAPI`, an in-memory fake of the Admin API endpoints the provider calls, from an `httptest.Server`, and inject an `*anthropic.Client` built with `newClient` and pointed at it through the package-level `testAPIClient`, which bypasses provider configuration entirely. Acceptance-style tests run Terraform itself, so they need `TF_ACC=1`.

```bash
TF_ACC=1 mise exec -- go test ./internal/provider/ -v -run TestAccWorkspace
```

`mise run test:live` runs the `TestLive_*` tests against a real organization. They need `ANTHROPIC_API_KEY` or `ANTHROPIC_AUTH_TOKEN` and create uniquely named workspaces, which destroy archives rather than deletes.

## Code layout

All resources live in the flat `internal/provider/` package, named `resource_<name>.go` with tests alongside as `<file>_test.go`. New resources must be registered in the `Resources()` or `DataSources()` method in `provider.go`, or the provider will not expose them.

## Commits

Commits follow [Conventional Commits](https://www.conventionalcommits.org) and require a [DCO](https://developercertificate.org) sign-off:

```bash
git commit -s -m "fix(workspace): keep unmanaged members on update"
```

The commit type determines the next version, so it is worth getting right.

## Releases

[release-please](https://github.com/googleapis/release-please) reads the conventional commits merged into `main` and maintains an open release pull request with the computed version bump and changelog entries. Merging that pull request tags the release and publishes the provider archives, plus a GPG-signed checksum file, via [GoReleaser](https://goreleaser.com). No release happens without that pull request being merged.

Each release carries the assets the provider registry protocol expects: one zip per platform, a `SHA256SUMS` file, a detached GPG signature over it, and `terraform-provider-anthropic_<version>_manifest.json` built from `terraform-registry-manifest.json` at the repository root. That manifest declares plugin protocol 6, which `providerserver.Serve` uses because `main.go` leaves `ProtocolVersion` unset. Registries assume protocol 5.0 when the manifest is missing, so a release without it installs and then fails to load.
