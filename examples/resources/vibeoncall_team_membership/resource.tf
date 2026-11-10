data "vibeoncall_member" "ada" {
  email = "ada@example.com"
}

resource "vibeoncall_team_membership" "ada" {
  team_id = vibeoncall_team.sre.id
  user_id = data.vibeoncall_member.ada.id
  role    = "admin"
}
