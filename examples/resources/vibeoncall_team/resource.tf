data "vibeoncall_member" "ada" {
  email = "ada@example.com"
}

data "vibeoncall_member" "grace" {
  email = "grace@example.com"
}

resource "vibeoncall_team" "sre" {
  name        = "SRE"
  description = "Site reliability engineering"

  members = [
    { user_id = data.vibeoncall_member.ada.id, role = "admin" },
    { user_id = data.vibeoncall_member.grace.id },
  ]
}
