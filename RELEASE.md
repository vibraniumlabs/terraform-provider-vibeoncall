# Releasing

Pushing a `v*` tag runs the `Release` workflow. It waits for a second maintainer to approve the `release` environment, then GoReleaser builds and signs the provider into a draft GitHub release, with that version's `CHANGELOG.md` entry as its notes. Publishing the draft is what the Terraform Registry picks up.

## Before you tag

1. **The API is in production.** `main` follows ai-server `dev`, so it can call endpoints production doesn't serve yet. Build it against the production spec, then discard the regenerated client:

   ```shell
   make update-client-main
   go build ./... && go test ./...
   git checkout -- internal/client
   ```

   If the build or tests fail, the provider depends on something production doesn't serve yet; wait for the next ai-server promotion.

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

## Patch release while `main` waits on the API

When a released version needs a fix before ai-server's next promotion, and `main` already calls endpoints production doesn't serve, release the fix from a branch off the last tag:

```shell
git switch -c release/v0.1.x v0.1.0
git cherry-pick <fix>
```

Check it against production as in [Before you tag](#before-you-tag), step 1. Add the patch version to `CHANGELOG.md` on that branch, push it, and tag the branch's head as in [Tag](#tag). Then add the same `CHANGELOG.md` entry to `main`.

## When a release is wrong

If the draft is wrong, delete the draft and its tag, fix `main`, and tag again; nothing has been published yet. Once a release is published, don't move or delete its tag; Terraform and OpenTofu caches keep the old checksums. Fix it on `main` and release the next patch version.

## Signing key

GoReleaser signs each release with the GPG key stored in the `release` environment secrets `GPG_PRIVATE_KEY` and `PASSPHRASE`. Its public key is registered in the Terraform Registry and the OpenTofu registry, so rotating the key means updating both registries as well as the secrets.
