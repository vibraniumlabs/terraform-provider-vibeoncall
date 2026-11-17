## 0.1.1 (Unreleased)

NOTES:

* resource/vibeoncall_team_membership, resource/vibeoncall_team: Removing a member who is still in one of the team's schedule rotations, upcoming overrides, escalation policy tiers or alert routes now fails with an error that lists each of them, instead of leaving the member paged there. Reference such a member as `vibeoncall_team_membership.<name>.user_id` with `create_before_destroy`, as `user_ids` on `vibeoncall_schedule`, `vibeoncall_escalation_policy` and `vibeoncall_alert_route` describe, so Terraform takes them out of those first.

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
