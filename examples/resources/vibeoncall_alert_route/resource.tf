resource "vibeoncall_alert_route" "payments" {
  name        = "Payments"
  description = "Pages the payments on-call for production alerts."
  team_id     = vibeoncall_team.payments.id

  # The first rule whose conditions match decides.
  rules = [
    {
      name = "Ignore staging"
      condition_groups = [{
        operator   = "AND"
        conditions = [{ field = { field_path = "details.env", operator = "exact_match", value = "staging" } }]
      }]
      do_nothing = true
    },
    {
      name = "SEV0 and SEV1 page the escalation policy"
      condition_groups = [{
        operator   = "AND"
        conditions = [{ severity = { operator = "greater_than_or_equal", severity = "SEV1" } }]
      }]
      escalate = {
        escalation_policy_ids = [vibeoncall_escalation_policy.payments.id]
        urgency               = "HIGH"
      }
    },
    {
      name = "Repeated alerts out of hours"
      condition_groups = [{
        operator = "AND"
        conditions = [
          { count = { threshold = 3, timeframe_minutes = 10 } },
          { time = { timezone = "Asia/Taipei", daily = { start_time = "19:00", end_time = "09:00" } } },
        ]
      }]
      escalate = {
        schedule_ids  = [vibeoncall_schedule.payments.id]
        delay_minutes = 5
      }
    },
    {
      # Without condition_groups, the last rule matches every alert no earlier rule matched.
      name = "Everything else"
      escalate = {
        schedule_ids = [vibeoncall_schedule.payments.id]
        urgency      = "LOW"
      }
    },
  ]
}

resource "vibeoncall_alert_source" "datadog" {
  name             = "Datadog production"
  type             = "datadog"
  team_id          = vibeoncall_team.payments.id
  alert_routing_id = vibeoncall_alert_route.payments.id
}
