# Contributing

This is an assessment project, but it follows the same workflow as a small production repository. Read [AGENTS.md](AGENTS.md) first.

## Workflow

1. Open an issue with a clear goal and acceptance criteria.
2. Create a branch from `main`.
3. Make a small, focused change.
4. Add or update tests.
5. Open a pull request that links the issue.
6. Wait for AI review, and for CI once the workflow lands in M0-5.
7. Address comments or explain why they do not apply.
8. Squash merge.

## Branch names

```text
feat/notification-api
feat/worker-retries
fix/rate-limit-key
docs/architecture
test/provider-contract
ci/openapi-lint
```

## Commit messages

Use Conventional Commits:

```text
feat: add notification creation endpoint
fix: classify provider 429 as transient
docs: document batch status rules
test: cover cancellation race
refactor: extract retry policy
chore: bump dependencies
ci: add integration test job
```

Keep commits buildable. Do not mix a refactor with a feature.

## Pull requests

Every PR answers:

- What changed?
- Why?
- How was it tested?
- What is out of scope?
- Which issue does it close?

The template is in [.github/PULL_REQUEST_TEMPLATE.md](.github/PULL_REQUEST_TEMPLATE.md).

## Review checklist

- [ ] The change closes an issue.
- [ ] `main` remains runnable.
- [ ] Tests cover the happy path and at least one failure path.
- [ ] Metrics and logs are added for new behaviour.
- [ ] The OpenAPI spec is updated.
- [ ] The README is still accurate.
- [ ] Errors map to the right HTTP status.
- [ ] Recipients are masked in logs.
- [ ] No secrets or personal data.
- [ ] The design can be explained in one minute.

## AI review

CodeRabbit and cubic review pull requests. The comments are part of the review record. Address them or reply with a reason. Do not merge with unresolved high-severity comments.

## Labels

| Label | Meaning |
| --- | --- |
| `type:feature` | New behaviour. |
| `type:bug` | Broken behaviour. |
| `type:docs` | Documentation. |
| `type:test` | Tests. |
| `area:api` | HTTP API. |
| `area:worker` | Queue and delivery. |
| `area:db` | Schema and migrations. |
| `area:ops` | Docker, CI, observability. |
| `priority:high` | Blocks the MVP. |
| `priority:low` | Nice to have. |
| `bonus` | Bonus feature. |

## Local checks

These targets arrive with the Go module (M0-2) and the CI workflow (M0-5).

```bash
make fmt
make lint
make test
make openapi-lint
```

If Make is unavailable:

```bash
gofmt -w .
go vet ./...
go test -race ./...
```
