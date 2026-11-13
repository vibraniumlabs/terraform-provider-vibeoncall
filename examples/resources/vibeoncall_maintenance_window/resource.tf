resource "vibeoncall_maintenance_window" "db_upgrade" {
  name        = "Postgres upgrade"
  description = "Primary database failover and upgrade."
  team_id     = vibeoncall_team.sre.id
  start_at    = "2026-11-20T09:00:00+08:00"
  end_at      = "2026-11-20T11:00:00+08:00"

  announcement_channels = [data.vibeoncall_chat_channel.incidents.id]

  # Covers Datadog or Grafana alerts at SEV3 or less severe: SEV3, SEV4, SEV5 and SIGNAL.
  condition_groups = [{
    operator = "AND"
    conditions = [
      { provider = { providers = ["datadog", "grafana"] } },
      { severity = { operator = "less_than_or_equal", severity = "SEV3" } },
    ]
  }]
}

resource "vibeoncall_maintenance_window" "staging_nights" {
  name     = "Staging, nights and weekends"
  start_at = "2026-11-20T00:00:00Z"
  end_at   = "2026-12-20T00:00:00Z"

  # Groups are joined by OR: staging alerts at any time, or any alert at night or on weekends.
  condition_groups = [
    {
      operator = "AND"
      conditions = [
        { field = { field_path = "details.env", operator = "exact_match", value = "staging" } },
      ]
    },
    {
      operator = "OR"
      conditions = [
        { time = { timezone = "Asia/Taipei", daily = { start_time = "22:00", end_time = "07:00" } } },
        { time = { timezone = "Asia/Taipei", weekly = [{ start_day = "saturday", end_day = "sunday", start_time = "00:00", end_time = "23:59" }] } },
      ]
    },
  ]
}
