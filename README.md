# Terraform Provider for Vibe OnCall

Manage Vibe OnCall configuration as code with Terraform.

## Usage

```hcl
terraform {
  required_providers {
    vibeoncall = {
      source = "vibraniumlabs/vibeoncall"
    }
  }
}

provider "vibeoncall" {}
```

Set the API key with the `VIBEONCALL_API_KEY` environment variable, or with `api_key` in the provider block. See [`docs/`](docs/) for every resource and attribute.

## Development

Requirements: [Go](https://go.dev/doc/install) (version in `go.mod`) and [Terraform](https://developer.hashicorp.com/terraform/downloads).

```shell
go build -o bin/terraform-provider-vibeoncall .
```

To run Terraform against the local build, point a CLI config at `bin/`:

```hcl
provider_installation {
  dev_overrides {
    "vibraniumlabs/vibeoncall" = "/path/to/terraform-provider-vibeoncall/bin"
  }
  direct {}
}
```

```shell
export TF_CLI_CONFIG_FILE=/path/to/dev.tfrc
export VIBEONCALL_API_KEY=...
terraform plan
```

### Regenerating code

- API client: `go generate ./internal/client` regenerates `internal/client/client.gen.go` from `internal/client/openapi.yaml`.
- Docs: `make generate` regenerates `docs/` from the provider schema and `examples/`.
