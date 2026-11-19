resource "vibeoncall_alert_source" "datadog" {
  name    = "Datadog production"
  type    = "datadog"
  team_id = vibeoncall_team.payments.id

  lifecycle {
    create_before_destroy = true
  }
}

# Hand the webhook URL to the monitoring tool's own provider.
resource "datadog_webhook" "vibeoncall" {
  name      = "vibeoncall-payments"
  url       = vibeoncall_alert_source.datadog.webhook_url
  encode_as = "json"
}

# Configure this source's webhook_url in Jira Cloud's webhook settings.
resource "vibeoncall_alert_source" "jira" {
  name    = "Jira issues"
  type    = "jira"
  team_id = vibeoncall_team.payments.id

  lifecycle {
    create_before_destroy = true
  }
}
