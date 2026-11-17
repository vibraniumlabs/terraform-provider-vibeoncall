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
  admin_emails       = [for email in var.team_admin_emails : lower(email)]
  member_emails      = [for email in var.team_member_emails : lower(email)]
  participant_emails = [for email in var.rotation_participant_emails : lower(email)]
  roles              = merge({ for email in local.member_emails : email => "member" }, { for email in local.admin_emails : email => "admin" })
}

data "vibeoncall_member" "team" {
  for_each = local.roles
  email    = each.key
}

resource "vibeoncall_team" "this" {
  name        = var.team_name
  description = var.team_description
}

# One membership per person, so promoting a member to admin only changes their role.
# create_before_destroy makes Terraform stop paging someone before removing them.
resource "vibeoncall_team_membership" "team" {
  for_each = local.roles
  team_id  = vibeoncall_team.this.id
  user_id  = data.vibeoncall_member.team[each.key].id
  role     = each.value

  lifecycle {
    create_before_destroy = true
  }
}

resource "vibeoncall_schedule" "this" {
  name      = "${var.team_name} primary"
  time_zone = var.time_zone
  team_ids  = [vibeoncall_team.this.id]

  rotations = [
    {
      name            = "Weekly"
      start_date      = var.rotation_start_date
      participant_ids = [for email in local.participant_emails : vibeoncall_team_membership.team[email].user_id]
      weekly = {
        start_day  = "monday"
        start_time = "09:00"
        end_day    = "monday"
        end_time   = "09:00"
      }
    },
  ]
}

resource "vibeoncall_escalation_policy" "this" {
  name        = "${var.team_name} default"
  description = "Page whoever is on call, then the team admins."
  team_id     = vibeoncall_team.this.id

  tiers = [
    {
      acknowledgement_timeout_seconds = 600
      schedule_targets                = [{ schedule_id = vibeoncall_schedule.this.id }]
    },
    {
      acknowledgement_timeout_seconds = 900
      user_ids                        = [for email in local.admin_emails : vibeoncall_team_membership.team[email].user_id]
    },
  ]
}

resource "vibeoncall_team_default_escalation_policy" "this" {
  team_id              = vibeoncall_team.this.id
  escalation_policy_id = vibeoncall_escalation_policy.this.id
}

resource "vibeoncall_service" "this" {
  name        = var.service_name
  description = var.service_description
  team_id     = vibeoncall_team.this.id
}

resource "vibeoncall_alert_route" "this" {
  name        = var.team_name
  description = "Pages the escalation policy for SEV0 and SEV1, and whoever is on call for everything else."
  team_id     = vibeoncall_team.this.id

  rules = [
    {
      name = "SEV0 and SEV1"
      condition_groups = [{
        operator   = "AND"
        conditions = [{ severity = { operator = "greater_than_or_equal", severity = "SEV1" } }]
      }]
      escalate = {
        escalation_policy_ids = [vibeoncall_escalation_policy.this.id]
        urgency               = "HIGH"
      }
    },
    {
      name = "Everything else"
      escalate = {
        schedule_ids = [vibeoncall_schedule.this.id]
        urgency      = "LOW"
      }
    },
  ]
}

resource "vibeoncall_alert_source" "monitoring" {
  name             = "${var.team_name} ${var.alert_source_type}"
  type             = var.alert_source_type
  team_id          = vibeoncall_team.this.id
  alert_routing_id = vibeoncall_alert_route.this.id
}

output "alert_source_webhook_url" {
  description = "URL to configure in the monitoring tool. Read it with `terraform output -raw alert_source_webhook_url`."
  value       = vibeoncall_alert_source.monitoring.webhook_url
  sensitive   = true
}
