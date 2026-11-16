# Releasing

Pushing a `v*` tag runs the `Release` workflow. It waits for a second maintainer to approve the `release` environment, then GoReleaser builds and signs the provider into a draft GitHub release, with that version's `CHANGELOG.md` entry as its notes. Publishing the draft is what the Terraform Registry picks up.

## Before you tag

1. **The API is in production.** Every endpoint the provider calls must already be on ai-server `main`. Copy the spec from `main` and regenerate the client:

   ```shell
   gh api 'repos/vibraniumlabs/ai-server/contents/openapi/openapi.yaml?ref=main' \
     -H 'Accept: application/vnd.github.raw' > internal/client/openapi.yaml
   go generate ./internal/client
   go build ./... && go test ./...
   ```

   If the build or tests fail, the provider depends on something production doesn't serve yet; wait for the next ai-server promotion. If anything changed, merge it in a PR before tagging.

2. **The complete example works against production.** In `examples/complete`, `terraform apply` and then `terraform plan` shows no changes. Destroy it afterwards.

3. **The changelog is final.** Its entry becomes the release notes, so write it for users. Open a PR that replaces `(Unreleased)` in `CHANGELOG.md` with the release date, for example `## 0.1.0 (November 20, 2026)`, and merge it. The workflow fails if the entry is missing or still says `(Unreleased)`.

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

## When a release is wrong

If the draft is wrong, delete the draft and its tag, fix `main`, and tag again; nothing has been published yet. Once a release is published, don't move or delete its tag; Terraform and OpenTofu caches keep the old checksums. Fix it on `main` and release the next patch version.

## Signing key

GoReleaser signs each release with the GPG key stored in the `release` environment secrets `GPG_PRIVATE_KEY` and `PASSPHRASE`. Its public key is registered in the Terraform Registry and the OpenTofu registry, so rotating the key means updating both registries as well as the secrets.
