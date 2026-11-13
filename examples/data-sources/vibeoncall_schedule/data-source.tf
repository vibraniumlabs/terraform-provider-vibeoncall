data "vibeoncall_schedule" "platform" {
  name    = "Platform primary"
  team_id = data.vibeoncall_team.platform.id
}
