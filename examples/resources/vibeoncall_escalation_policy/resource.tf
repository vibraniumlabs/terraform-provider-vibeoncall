data "vibeoncall_member" "ada" {
  email = "ada@example.com"
}

resource "vibeoncall_team_membership" "ada" {
  team_id = vibeoncall_team.sre.id
  user_id = data.vibeoncall_member.ada.id

  lifecycle {
    create_before_destroy = true
  }
}

data "vibeoncall_member" "grace" {
  email = "grace@example.com"
}

# On Microsoft Teams, also set microsoft_teams_team_id on the channel lookup.
data "vibeoncall_chat_channel" "sre_alerts" {
  name = "sre-alerts"
}

resource "vibeoncall_escalation_policy" "sre" {
  name        = "SRE primary"
  description = "Post to the SRE channel, then page the primary on-call, then Ada, then the platform lead."
  team_id     = vibeoncall_team.sre.id

  tiers = [
    {
      # Anyone in the channel can acknowledge from the card before the on-call is paged.
      acknowledgement_timeout_seconds = 120
      channel_targets = [{
        channel_id = data.vibeoncall_chat_channel.sre_alerts.id
        name       = data.vibeoncall_chat_channel.sre_alerts.name
      }]
    },
    {
      acknowledgement_timeout_seconds = 300
      schedule_targets                = [{ schedule_id = "12" }]
    },
    {
      # Ada is on the SRE team, so reference her membership.
      acknowledgement_timeout_seconds = 600
      user_ids                        = [vibeoncall_team_membership.ada.user_id]
    },
    {
      # Grace is not on the SRE team, so reference the data source.
      acknowledgement_timeout_seconds = 900
      user_ids                        = [data.vibeoncall_member.grace.id]
    },
  ]
}
