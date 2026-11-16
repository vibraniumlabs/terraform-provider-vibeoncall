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

data "vibeoncall_member" "alan" {
  email = "alan@example.com"
}

resource "vibeoncall_team_membership" "alan" {
  team_id = vibeoncall_team.sre.id
  user_id = data.vibeoncall_member.alan.id

  lifecycle {
    create_before_destroy = true
  }
}

resource "vibeoncall_schedule" "sre" {
  name      = "SRE primary"
  time_zone = "America/New_York"
  team_ids  = [vibeoncall_team.sre.id]

  rotations = [
    {
      # Hands off every Monday at 09:00 and covers the whole week.
      name            = "Weekly"
      start_date      = "2026-09-07"
      participant_ids = [vibeoncall_team_membership.ada.user_id, vibeoncall_team_membership.alan.user_id]
      weekly = {
        start_day  = "monday"
        start_time = "09:00"
        end_day    = "monday"
        end_time   = "09:00"
      }
    },
    {
      name            = "Weekend"
      start_date      = "2026-09-05"
      participant_ids = [vibeoncall_team_membership.alan.user_id]
      weekly_windows = {
        windows = [
          { start_day = "saturday", start_time = "00:00", end_day = "sunday", end_time = "23:00" },
        ]
      }
    },
  ]
}
