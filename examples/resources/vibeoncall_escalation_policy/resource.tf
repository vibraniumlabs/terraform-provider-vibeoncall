data "vibeoncall_member" "ada" {
  email = "ada@example.com"
}

resource "vibeoncall_team_membership" "ada" {
  team_id = vibeoncall_team.sre.id
  user_id = data.vibeoncall_member.ada.id
}

data "vibeoncall_member" "grace" {
  email = "grace@example.com"
}

resource "vibeoncall_escalation_policy" "sre" {
  name        = "SRE primary"
  description = "Page the primary on-call, then Ada, then the platform lead."
  team_id     = vibeoncall_team.sre.id

  tiers = [
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
