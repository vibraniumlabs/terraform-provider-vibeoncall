variable "team_name" {
  description = "Team name. Team names are not unique, so pick one that isn't already in use."
  type        = string
  default     = "Payments"
}

variable "service_name" {
  description = "Service name, unique within the organization."
  type        = string
  default     = "Checkout"
}

variable "lead_email" {
  description = "Email of an existing user who leads the team. Paged when the on-call engineer doesn't acknowledge."
  type        = string
}

variable "on_call_emails" {
  description = "Emails of existing users in the weekly rotation, in the order they take turns. Don't include lead_email."
  type        = list(string)

  validation {
    condition     = length(var.on_call_emails) > 0
    error_message = "The rotation needs at least one participant."
  }

  validation {
    condition     = length(distinct([for email in var.on_call_emails : lower(email)])) == length(var.on_call_emails)
    error_message = "Each on-call engineer can appear only once."
  }

  validation {
    condition     = !contains([for email in var.on_call_emails : lower(email)], lower(var.lead_email))
    error_message = "lead_email is already a team admin; list only the other on-call engineers."
  }
}

variable "time_zone" {
  description = "IANA time zone the rotation hands off in."
  type        = string
  default     = "America/New_York"
}

variable "rotation_start_date" {
  description = "Date the first participant goes on call, in YYYY-MM-DD format. Pick a Monday, since the rotation hands off on Mondays at 09:00."
  type        = string
}

variable "alert_source_type" {
  description = "Monitoring tool that sends alerts to the team, such as `datadog`, `grafana` or `http`."
  type        = string
  default     = "datadog"

  validation {
    condition     = var.alert_source_type != "email"
    error_message = "An email source has no webhook URL; this example outputs one."
  }
}
