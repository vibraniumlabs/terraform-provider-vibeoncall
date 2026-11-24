## 0.1.1 (Unreleased)

ENHANCEMENTS:

* resource/vibeoncall_alert_source: Configure email deduplication with a string template using subject, sender, and body variables. Omit or use null for the provider default; an empty string disables matching. Removing a template restores the default.
* resource/vibeoncall_alert_source: Accept `type = "jira"` for Jira Cloud webhook sources. Upgrade the provider to this version or later to configure Jira sources.
* resource/vibeoncall_alert_route, resource/vibeoncall_maintenance_window: Accept `jira` in provider conditions.
* resource/vibeoncall_schedule: Add `hand_off_time` to `weekly` and `weekly_windows` to hand off at a set time on `hand_off_day` instead of at 00:00.

NOTES:

* resource/vibeoncall_schedule: Upgrade from 0.1.0 if anyone sets handoff times in the web app. Version 0.1.0 doesn't know `hand_off_time`, so whenever it updates a weekly rotation, it resets that rotation's handoff time to 00:00 without showing it in the plan. From this version, the plan shows a time set in the web app as a change, and applying without `hand_off_time` resets it to 00:00. Set `hand_off_time` to keep it.
* resource/vibeoncall_team_membership, resource/vibeoncall_team: Removing a member who is still in one of the team's schedule rotations, upcoming overrides, escalation policy tiers or alert routes now fails with an error that lists each of them, instead of leaving the member paged there.
  * With `vibeoncall_team_membership`, reference the member as `vibeoncall_team_membership.<name>.user_id` with `create_before_destroy`, as `participant_ids` on `vibeoncall_schedule` and `user_ids` on `vibeoncall_escalation_policy` and `vibeoncall_alert_route` describe, so Terraform takes them out of those first.
  * With `members` on `vibeoncall_team`, Terraform updates the team before what references it, so `create_before_destroy` doesn't help: take the member out of the schedule, policy or route in an earlier apply, or manage members with `vibeoncall_team_membership`.

## 0.1.0 (November 17, 2026)

FEATURES:

* **New Resource:** `vibeoncall_team`
* **New Resource:** `vibeoncall_team_membership`
* **New Resource:** `vibeoncall_escalation_policy`
* **New Resource:** `vibeoncall_team_default_escalation_policy`
* **New Resource:** `vibeoncall_schedule`
* **New Resource:** `vibeoncall_service`
* **New Resource:** `vibeoncall_maintenance_window`
* **New Resource:** `vibeoncall_alert_source`
* **New Resource:** `vibeoncall_alert_route`
* **New Data Source:** `vibeoncall_member`
* **New Data Source:** `vibeoncall_team`
* **New Data Source:** `vibeoncall_label`
* **New Data Source:** `vibeoncall_chat_channel`
* **New Data Source:** `vibeoncall_schedule`
