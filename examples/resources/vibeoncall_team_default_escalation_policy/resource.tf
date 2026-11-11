resource "vibeoncall_team_default_escalation_policy" "sre" {
  team_id              = vibeoncall_team.sre.id
  escalation_policy_id = vibeoncall_escalation_policy.sre.id
}
