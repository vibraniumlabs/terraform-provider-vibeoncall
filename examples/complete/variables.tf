variable "team_name" {
  description = "Team name. Team names are not unique, so pick one that isn't already in use."
  type        = string
}

variable "team_description" {
  description = "Team description."
  type        = string
  default     = null
}

variable "service_name" {
  description = "Service name, unique within the organization."
  type        = string
}

variable "service_description" {
  description = "Service description."
  type        = string
  default     = null
}

variable "team_admin_emails" {
  description = "Emails of existing users who are team admins. The escalation policy pages all of them when whoever is on call doesn't acknowledge."
  type        = list(string)

  validation {
    condition     = length(var.team_admin_emails) > 0
    error_message = "The team needs at least one admin."
  }

  validation {
    condition     = length(distinct([for email in var.team_admin_emails : lower(email)])) == length(var.team_admin_emails)
    error_message = "Each team admin can appear only once."
  }
}

variable "team_member_emails" {
  description = "Emails of existing users who are team members."
  type        = list(string)
  default     = []

  validation {
    condition     = length(distinct([for email in var.team_member_emails : lower(email)])) == length(var.team_member_emails)
    error_message = "Each team member can appear only once."
  }

  validation {
    condition     = length(setintersection([for email in var.team_member_emails : lower(email)], [for email in var.team_admin_emails : lower(email)])) == 0
    error_message = "A user can be a team admin or a team member, not both."
  }
}

variable "rotation_participant_emails" {
  description = "Emails of team admins or members who take turns on the weekly rotation, in this order."
  type        = list(string)

  validation {
    condition     = length(var.rotation_participant_emails) > 0
    error_message = "The rotation needs at least one participant."
  }

  validation {
    condition     = length(distinct([for email in var.rotation_participant_emails : lower(email)])) == length(var.rotation_participant_emails)
    error_message = "Each rotation participant can appear only once."
  }

  validation {
    condition     = alltrue([for email in var.rotation_participant_emails : contains([for e in concat(var.team_admin_emails, var.team_member_emails) : lower(e)], lower(email))])
    error_message = "Every rotation participant must be in team_admin_emails or team_member_emails."
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
  description = "Tool that sends alerts to the team, such as `datadog`, `grafana`, `jira` or `http`."
  type        = string
  default     = "datadog"

  validation {
    condition     = var.alert_source_type != "email"
    error_message = "An email source has no webhook URL; this example outputs one."
  }
}
