# Contributing to threadwatch

Thanks for your interest. threadwatch is a small, focused tool; contributions
that keep it that way are the most welcome.

## Developer Certificate of Origin (DCO)

All commits must be signed off, certifying you have the right to submit the
work under the project's [Apache 2.0](LICENSE) license (see
[developercertificate.org](https://developercertificate.org/)):

```bash
git commit -s -m "fix: handle 304 with empty ETag"
```

`-s` appends a `Signed-off-by: Your Name <you@example.com>` trailer. Set your
`user.name` / `user.email` first. PRs whose commits aren't signed off will be
asked to amend.

## Local development

```bash
make run         # run threadwatch locally with default config
make test        # go test ./... -race
make lint        # golangci-lint (install it first; see .golangci.yml)
make vet         # go vet ./...
make docker      # build the container image
make helm-lint   # lint the Helm chart
```

CI runs the same lint, race tests, helm lint/template, and Trivy scans, then
builds the multi-arch image. Run `make lint test` before opening a PR to catch
the common failures locally.

## Pull request conventions

- **Commit messages** follow [Conventional Commits](https://www.conventionalcommits.org/):
  `feat:`, `fix:`, `ci:`, `docs:`, `refactor:`, `test:`, optionally scoped
  (`docs(chart): ...`). One logical change per commit; explain the *why* in the
  body when it isn't obvious.
- **Tests.** The diff logic in `internal/poller` is the correctness core — new
  behavior there needs a table-driven test case. Other packages get tests
  opportunistically; don't regress coverage on the poller/diff path.
- **Keep the surface small.** New config, endpoints, or dependencies should
  earn their place. When in doubt, open an issue first.
- **Update the [CHANGELOG](CHANGELOG.md)** `Unreleased` section with a line for
  user-visible changes.

## Design context

Read the **"Design decisions worth knowing"** section of the
[README](README.md) before proposing structural changes — it covers why
threadwatch uses SQLite, an in-process poller, server-rendered htmx, and ETag
conditional requests. Those choices are deliberate; changing one is a
discussion worth having up front.
