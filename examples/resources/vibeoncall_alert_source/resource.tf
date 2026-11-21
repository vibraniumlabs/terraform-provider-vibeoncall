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

# Each email creates its own alert; normal alert routing still applies.
resource "vibeoncall_alert_source" "email" {
  name             = "Email alerts"
  type             = "email"
  team_id          = vibeoncall_team.payments.id
  alert_routing_id = vibeoncall_alert_route.payments.id
  deduplication    = ""
}

# Omit deduplication (or set null) to match by subject within this source.
# To include the sender, use deduplication = "{{subject}}-{{sender}}".
# Templates and populated matching keys are limited to 500 characters.
