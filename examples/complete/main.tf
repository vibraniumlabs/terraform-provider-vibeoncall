terraform {
  required_version = ">= 1.9"

  required_providers {
    vibeoncall = {
      source  = "vibraniumlabs/vibeoncall"
      version = "~> 0.1.0"
    }
  }
}

provider "vibeoncall" {}

locals {
  lead_email     = lower(var.lead_email)
  on_call_emails = [for email in var.on_call_emails : lower(email)]
  member_roles   = merge({ for email in local.on_call_emails : email => "member" }, { (local.lead_email) = "admin" })
}

data "vibeoncall_member" "team" {
  for_each = local.member_roles
  email    = each.key
}

resource "vibeoncall_team" "payments" {
  name        = var.team_name
  description = "Owns checkout and billing."
}

# One membership per person, so promoting someone to lead only changes their role.
# create_before_destroy makes Terraform stop paging someone before removing them.
resource "vibeoncall_team_membership" "team" {
  for_each = local.member_roles
  team_id  = vibeoncall_team.payments.id
  user_id  = data.vibeoncall_member.team[each.key].id
  role     = each.value

  lifecycle {
    create_before_destroy = true
  }
}

resource "vibeoncall_schedule" "payments" {
  name      = "${var.team_name} primary"
  time_zone = var.time_zone
  team_ids  = [vibeoncall_team.payments.id]

  rotations = [
    {
      name            = "Weekly"
      start_date      = var.rotation_start_date
      participant_ids = [for email in local.on_call_emails : vibeoncall_team_membership.team[email].user_id]
      weekly = {
        start_day  = "monday"
        start_time = "09:00"
        end_day    = "monday"
        end_time   = "09:00"
      }
    },
  ]
}

resource "vibeoncall_escalation_policy" "payments" {
  name        = "${var.team_name} default"
  description = "Page the on-call engineer, then the team lead."
  team_id     = vibeoncall_team.payments.id

  tiers = [
    {
      acknowledgement_timeout_seconds = 600
      schedule_targets                = [{ schedule_id = vibeoncall_schedule.payments.id }]
    },
    {
      acknowledgement_timeout_seconds = 900
      user_ids                        = [vibeoncall_team_membership.team[local.lead_email].user_id]
    },
  ]
}

resource "vibeoncall_team_default_escalation_policy" "payments" {
  team_id              = vibeoncall_team.payments.id
  escalation_policy_id = vibeoncall_escalation_policy.payments.id
}

resource "vibeoncall_service" "checkout" {
  name        = var.service_name
  description = "Takes payments for every storefront."
  team_id     = vibeoncall_team.payments.id
}

resource "vibeoncall_alert_route" "payments" {
  name        = var.team_name
  description = "Pages the escalation policy for SEV0 and SEV1, and the on-call engineer for everything else."
  team_id     = vibeoncall_team.payments.id

  rules = [
    {
      name = "SEV0 and SEV1"
      condition_groups = [{
        operator   = "AND"
        conditions = [{ severity = { operator = "greater_than_or_equal", severity = "SEV1" } }]
      }]
      escalate = {
        escalation_policy_ids = [vibeoncall_escalation_policy.payments.id]
        urgency               = "HIGH"
      }
    },
    {
      name = "Everything else"
      escalate = {
        schedule_ids = [vibeoncall_schedule.payments.id]
        urgency      = "LOW"
      }
    },
  ]
}

resource "vibeoncall_alert_source" "monitoring" {
  name             = "${var.team_name} ${var.alert_source_type}"
  type             = var.alert_source_type
  team_id          = vibeoncall_team.payments.id
  alert_routing_id = vibeoncall_alert_route.payments.id
}

output "alert_source_webhook_url" {
  description = "URL to configure in the monitoring tool. Read it with `terraform output -raw alert_source_webhook_url`."
  value       = vibeoncall_alert_source.monitoring.webhook_url
  sensitive   = true
}
