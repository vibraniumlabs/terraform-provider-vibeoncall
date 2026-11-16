data "vibeoncall_member" "ada" {
  email = "ada@example.com"
}

resource "vibeoncall_team_membership" "ada" {
  team_id = vibeoncall_team.payments.id
  user_id = data.vibeoncall_member.ada.id

  lifecycle {
    create_before_destroy = true
  }
}

data "vibeoncall_member" "grace" {
  email = "grace@example.com"
}

resource "vibeoncall_service" "checkout" {
  name        = "Checkout"
  description = "Takes payments for every storefront."
  team_id     = vibeoncall_team.payments.id

  responder_team_ids = [vibeoncall_team.platform.id]

  # Ada is on the owning team, so reference her membership.
  responder_user_ids = [vibeoncall_team_membership.ada.user_id]

  stakeholder_user_ids = [data.vibeoncall_member.grace.id]
}
