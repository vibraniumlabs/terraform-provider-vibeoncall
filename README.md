# Terraform Provider for Vibe OnCall

Manage Vibe OnCall configuration as code with Terraform or OpenTofu: teams and their members, schedules, escalation policies, services, maintenance windows, alert sources and alert routes.

- Documentation: [Terraform Registry](https://registry.terraform.io/providers/vibraniumlabs/vibeoncall/latest/docs)
- Complete example: [`examples/complete`](examples/complete), a team with a weekly rotation, a two-tier escalation policy, a service, and an alert source feeding an alert route

## Usage

```hcl
terraform {
  required_providers {
    vibeoncall = {
      source  = "vibraniumlabs/vibeoncall"
      version = "~> 0.1.0"
    }
  }
}

provider "vibeoncall" {}

resource "vibeoncall_team" "sre" {
  name = "SRE"
}
```

Set the API key with the `VIBEONCALL_API_KEY` environment variable, or with `api_key` in the provider block.

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

## Releasing

See [RELEASE.md](RELEASE.md).
