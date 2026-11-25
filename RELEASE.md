# Releasing

Pushing a `v*` tag runs the `Release` workflow. It waits for a second maintainer to approve the `release` environment, then GoReleaser builds and signs the provider into a draft GitHub release, with that version's `CHANGELOG.md` entry as its notes. Publishing the draft is what the Terraform Registry picks up.

## Before you tag

1. **The API is in production.** `main` follows ai-server `dev`, so it can call endpoints production doesn't serve yet. Regenerate the client from the production spec and keep it, so the release ships the client you checked:

   ```shell
   make update-client-main
   go build ./... && go test ./...
   ```

   If the build or tests fail, the provider depends on something production doesn't serve yet; wait for the next ai-server promotion.

2. **The release build works against production.** Build the provider from step 1 and run Terraform against it as in [Development](README.md#development), so the check doesn't download a released version from the Registry. Leave `VIBEONCALL_BASE_URL` unset, so it calls production. In `examples/complete`, fill in `terraform.tfvars` with users that exist in production and skip `terraform init`. `terraform apply` must warn that provider development overrides are in effect; otherwise it's running a released version. Then `terraform plan` shows no changes. A spec on `main` doesn't prove a feature is on in production, so also try each resource or attribute in this release's changelog entry that the example doesn't use. Destroy everything afterwards.

   ```shell
   go build -o bin/terraform-provider-vibeoncall .
   printf 'provider_installation {\n  dev_overrides {\n    "vibraniumlabs/vibeoncall" = "%s/bin"\n  }\n  direct {}\n}\n' "$PWD" > bin/dev.tfrc
   export TF_CLI_CONFIG_FILE="$PWD/bin/dev.tfrc"
   export VIBEONCALL_API_KEY=...
   unset VIBEONCALL_BASE_URL
   cd examples/complete && terraform apply
   ```

3. **The changelog is final.** Its entry becomes the release notes, so write it for users. Open a PR that replaces `(Unreleased)` in `CHANGELOG.md` with the release date, for example `## 0.1.0 (November 20, 2026)`, and includes any `internal/client` change from step 1. Merge it. The workflow fails if the entry is missing or still says `(Unreleased)`.

## Tag

Tag the merge commit on `main` and push the tag. Only members of `@vibraniumlabs/terraform-provider-maintainers` and repository admins can create `v*` tags.

```shell
git switch main && git pull
git tag v0.1.0
git push origin v0.1.0
```

## Approve

The `Release` workflow stops at the `release` environment. Another maintainer opens the run in the Actions tab and approves it; whoever pushed the tag can't approve their own release.

## Check the draft, then publish

1. The draft release has a zip for every platform, `SHA256SUMS`, `SHA256SUMS.sig`, and the manifest, and its notes match the `CHANGELOG.md` entry.
2. Publish it:

   ```shell
   gh release edit v0.1.0 --draft=false
   ```

3. The new version appears on the [Registry](https://registry.terraform.io/providers/vibraniumlabs/vibeoncall/latest). For the first release only, publish the provider from HCP Terraform: **Registry** → **Public namespaces** → `vibraniumlabs` → **Publish** → **Provider**.
4. A clean `terraform init` installs the new version, and so does `tofu init` once the provider is in the OpenTofu registry.

## Patch release while `main` waits on the API

When a released version needs a fix before ai-server's next promotion, and `main` already calls endpoints production doesn't serve, release the fix from a branch off the last tag:

```shell
git switch -c release/v0.1.x v0.1.0
git cherry-pick <fix>
```

Check it against production as in [Before you tag](#before-you-tag), steps 1 and 2, and commit any `internal/client` change on that branch, since the fix may carry a spec from `dev`. Add the patch version to `CHANGELOG.md` on that branch, then push the branch and tag its head:

```shell
git push origin release/v0.1.x
git tag v0.1.1
git push origin v0.1.1
```

Approve and publish it as for any release. Then add the same `CHANGELOG.md` entry to `main`.

## When a release is wrong

If the draft is wrong, delete the draft and its tag, fix `main`, and tag again; nothing has been published yet. Once a release is published, don't move or delete its tag; Terraform and OpenTofu caches keep the old checksums. Fix it on `main` and release the next patch version.

## Signing key

GoReleaser signs each release with the GPG key stored in the `release` environment secrets `GPG_PRIVATE_KEY` and `PASSPHRASE`. Its public key is registered in the Terraform Registry and the OpenTofu registry, so rotating the key means updating both registries as well as the secrets.
