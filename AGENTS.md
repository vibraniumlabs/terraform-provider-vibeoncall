# Agent instructions

## This repository is public

Everything in it is published: code, docs, examples, commit messages, PR titles and descriptions, and review comments.

- Don't name customers, prospects or their people, and don't describe their setups, meetings or evaluations. When a change comes from a customer request, write "a customer", or cite the Linear ticket ID (such as `VIB-1234`) without its content.
- Don't paste Linear tickets, Slack threads, Notion pages or other internal documents into PRs, commits or code. Describe the change by what the provider does.
- Linking other `vibraniumlabs` repositories and their PRs is fine.
- Examples and tests use placeholder data: `example.com` emails and generic team, service and schedule names.

## Layout

- `internal/client/` is the generated API client. `openapi.yaml` there is a verbatim copy of ai-server's `openapi/openapi.yaml`; never edit it by hand. To resync, copy it from ai-server `main` (production) and run `go generate ./internal/client`. A copy from `dev` belongs only on a branch that waits for that change to reach production.
- `internal/provider/` holds the resources and data sources, written by hand on top of the client.
- `examples/resources/<type>/` and `examples/data-sources/<type>/` are rendered into the docs. `examples/complete/` is an end-to-end example that CI validates.
- `templates/` holds doc templates for the resources that need notes.
- `docs/` is generated. Don't edit it; change the schema descriptions, examples or templates instead.

## Commands

- `go build ./...`
- `go test ./...` runs every test against the fake API in `internal/provider/fake_api_test.go`, so it needs no API key or network.
- `make generate` formats `examples/` and regenerates `docs/`. It doesn't regenerate the client. CI fails when it leaves a diff.
- `golangci-lint run`

## Conventions

- Before adding a resource or attribute, find the closest existing one in `internal/provider/` and follow its patterns, helpers and tests.
- A change to a resource or data source comes with its schema descriptions, tests against the fake API, its example, and the regenerated docs. Anything a user would notice gets an entry under the unreleased version in `CHANGELOG.md`.
- Validate string lengths with `utf16LengthAtMost`: the API counts UTF-16 code units, as JavaScript does.
- Add a `~> **Note**` only for behavior that is surprising and silent, such as team names not being unique. Don't add one for a failure whose API error already explains it.
- A new attribute that takes user IDs gets the same guidance as `user_ids` on `vibeoncall_escalation_policy`: its description says to reference team members through `vibeoncall_team_membership` with `create_before_destroy`, and its example does it that way.
- The rules for which public API changes are safe live in ai-server's `ai-docs/how-to-add-api.md`.
