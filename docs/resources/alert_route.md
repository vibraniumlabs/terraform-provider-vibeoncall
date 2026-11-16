---
page_title: "vibeoncall_alert_route Resource - vibeoncall"
subcategory: ""
description: |-
  An alert routing: ordered rules that decide what happens to each alert an alert source receives. The first rule whose conditions match pages its targets, or does nothing.
---

# vibeoncall_alert_route (Resource)

An alert routing: ordered rules that decide what happens to each alert an alert source receives. The first rule whose conditions match pages its targets, or does nothing.

Point an alert source at the routing with its `alert_routing_id`. A routing and the sources that use it must be in the same scope: the same team, or both organization level.

`rules` is the whole rule list. An apply replaces every rule of the routing with the list in the configuration, so rules added in the app are removed, and rules turned off in the app are turned back on.

~> **Note** Destroying a routing also deletes the enrichment rules set up on it in the app. The API refuses to delete a routing while an alert source or heartbeat still uses it. Terraform deletes sources that reference the routing first, but a source or heartbeat created in the app has to be repointed or deleted there.

## Example Usage

```terraform
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
```

## Moving from Opsgenie

Opsgenie spreads what a routing holds across several resources. Each translates as follows:

| Opsgenie | Vibe OnCall |
|----------|-------------|
| `opsgenie_api_integration` and its `responders` | `vibeoncall_alert_source`, with `alert_routing_id` naming the routing |
| `opsgenie_team_routing_rule`, ordered by `order` | One entry of `rules`, in the same order |
| Its `criteria` | The rule's `condition_groups`: `match-all-conditions` is one group with `operator = "AND"`, `match-any-condition` is one group with `operator = "OR"` |
| Its `time_restriction` | A `time` condition in the same group |
| Its `notify` | The rule's `escalate`: `escalation_policy_ids`, `schedule_ids` or `user_ids` |
| The team's default routing rule | The last rule, without `condition_groups` |
| `opsgenie_integration_action` with `ignore` | A rule with `do_nothing = true`, placed before the rules that page |
| `priority` P1 to P5 | `severity` `SEV1` to `SEV5` |
| `extra-properties` with a `key` | A `field` condition with `field_path = "details.<key>"` |

For example, this Opsgenie setup:

```terraform
resource "opsgenie_integration_action" "datadog" {
  integration_id = opsgenie_api_integration.datadog.id

  ignore {
    name = "Ignore staging"
    filter {
      type = "match-all-conditions"
      conditions {
        field          = "extra-properties"
        key            = "env"
        operation      = "equals"
        expected_value = "staging"
      }
    }
  }
}

resource "opsgenie_team_routing_rule" "critical" {
  name    = "P1 pages the escalation"
  team_id = opsgenie_team.payments.id
  order   = 0

  criteria {
    type = "match-all-conditions"
    conditions {
      field          = "priority"
      operation      = "equals"
      expected_value = "P1"
    }
  }

  notify {
    type = "escalation"
    id   = opsgenie_escalation.payments.id
  }
}
```

becomes:

```terraform
resource "vibeoncall_alert_route" "payments" {
  name    = "Payments"
  team_id = vibeoncall_team.payments.id

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
      name = "P1 pages the escalation"
      condition_groups = [{
        operator   = "AND"
        conditions = [{ severity = { operator = "equal_to", severity = "SEV1" } }]
      }]
      escalate = { escalation_policy_ids = [vibeoncall_escalation_policy.payments.id] }
    },
  ]
}
```

Opsgenie integration actions that create, close or acknowledge alerts, or rewrite alert fields, have no counterpart here.

<!-- schema generated by tfplugindocs -->
## Schema

### Required

- `name` (String) Routing name, unique in its scope: the same team, or organization level.

### Optional

- `description` (String) Routing description. Remove the argument to clear it.
- `enabled` (Boolean) Whether the routing evaluates its rules. Defaults to `true`.
- `rules` (Attributes List) Rules in evaluation order. The first rule whose conditions match decides what happens to the alert. Omit to have the routing page no one. (see [below for nested schema](#nestedatt--rules))
- `team_id` (String) Numeric ID of the team the routing belongs to. Omit for an organization-level routing. Changing it replaces the routing.

### Read-Only

- `id` (String) Numeric alert routing ID.

<a id="nestedatt--rules"></a>
### Nested Schema for `rules`

Optional:

- `condition_groups` (Attributes List) Which alerts the rule matches. An alert matches when it matches any group. Omit on the last rule to match every alert that no earlier rule matched. (see [below for nested schema](#nestedatt--rules--condition_groups))
- `do_nothing` (Boolean) Set to `true` to create the alert without paging anyone.
- `escalate` (Attributes) Page the targets. Set at least one of `escalation_policy_ids`, `schedule_ids` or `user_ids`. (see [below for nested schema](#nestedatt--rules--escalate))
- `name` (String) Rule name.

<a id="nestedatt--rules--condition_groups"></a>
### Nested Schema for `rules.condition_groups`

Required:

- `conditions` (Attributes List) Conditions in the group. Each sets exactly one of `count`, `field`, `provider`, `severity` or `time`. (see [below for nested schema](#nestedatt--rules--condition_groups--conditions))
- `operator` (String) `AND` to require every condition in the group, `OR` to require any.

<a id="nestedatt--rules--condition_groups--conditions"></a>
### Nested Schema for `rules.condition_groups.conditions`

Optional:

- `count` (Attributes) Matches once `threshold` alerts arrived within `timeframe_minutes`. (see [below for nested schema](#nestedatt--rules--condition_groups--conditions--count))
- `field` (Attributes) Matches a field of the incoming alert payload. (see [below for nested schema](#nestedatt--rules--condition_groups--conditions--field))
- `provider` (Attributes) Matches alerts from any of the given alert sources. (see [below for nested schema](#nestedatt--rules--condition_groups--conditions--provider))
- `severity` (Attributes) Matches the alert severity. `SEV0` is the most severe and `SIGNAL` the least. (see [below for nested schema](#nestedatt--rules--condition_groups--conditions--severity))
- `time` (Attributes) Matches alerts that arrive within a recurring daily or weekly interval. (see [below for nested schema](#nestedatt--rules--condition_groups--conditions--time))

<a id="nestedatt--rules--condition_groups--conditions--count"></a>
### Nested Schema for `rules.condition_groups.conditions.count`

Required:

- `threshold` (Number) Number of alerts, at least 2.
- `timeframe_minutes` (Number) Window the alerts must arrive within, in minutes.


<a id="nestedatt--rules--condition_groups--conditions--field"></a>
### Nested Schema for `rules.condition_groups.conditions.field`

Required:

- `field_path` (String) Dot-path into the incoming payload. The alert source's own fields are under `details`, for example `details.env`. A path that matches nothing never matches.
- `operator` (String) One of `contains`, `exact_match`, `not_contains`, `matches_regex`.
- `value` (String) Value to compare against, or the regular expression for `matches_regex`.

Optional:

- `case_sensitive` (Boolean) Whether the comparison is case-sensitive.


<a id="nestedatt--rules--condition_groups--conditions--provider"></a>
### Nested Schema for `rules.condition_groups.conditions.provider`

Required:

- `providers` (Set of String) Alert source types, such as `datadog`, `grafana` or `opsgenie`.


<a id="nestedatt--rules--condition_groups--conditions--severity"></a>
### Nested Schema for `rules.condition_groups.conditions.severity`

Required:

- `operator` (String) One of `equal_to`, `less_than`, `greater_than`, `less_than_or_equal`, `greater_than_or_equal`. `greater_than` means more severe, so `less_than_or_equal` with `SEV3` matches SEV3, SEV4, SEV5 and SIGNAL.
- `severity` (String) One of `SEV0` to `SEV5`, or `SIGNAL`.


<a id="nestedatt--rules--condition_groups--conditions--time"></a>
### Nested Schema for `rules.condition_groups.conditions.time`

Required:

- `timezone` (String) IANA time zone the interval is in, such as `Asia/Taipei`.

Optional:

- `daily` (Attributes) Every day between two times. An interval whose end is before its start crosses midnight. (see [below for nested schema](#nestedatt--rules--condition_groups--conditions--time--daily))
- `weekly` (Attributes List) Weekly ranges. The condition matches inside any of them. (see [below for nested schema](#nestedatt--rules--condition_groups--conditions--time--weekly))

<a id="nestedatt--rules--condition_groups--conditions--time--daily"></a>
### Nested Schema for `rules.condition_groups.conditions.time.daily`

Required:

- `end_time` (String) End time. A 24-hour time in `HH:MM` format, such as `17:30`.
- `start_time` (String) Start time. A 24-hour time in `HH:MM` format, such as `17:30`.


<a id="nestedatt--rules--condition_groups--conditions--time--weekly"></a>
### Nested Schema for `rules.condition_groups.conditions.time.weekly`

Required:

- `end_day` (String) Day the range ends. A lowercase weekday name, such as `monday`.
- `end_time` (String) End time on `end_day`. A 24-hour time in `HH:MM` format, such as `17:30`.
- `start_day` (String) Day the range starts. A lowercase weekday name, such as `monday`.
- `start_time` (String) Start time on `start_day`. A 24-hour time in `HH:MM` format, such as `17:30`.





<a id="nestedatt--rules--escalate"></a>
### Nested Schema for `rules.escalate`

Optional:

- `delay_minutes` (Number) Minutes to wait before paging, from 1 to 1440.
- `escalation_policy_ids` (Set of String) Numeric IDs of escalation policies to page.
- `schedule_ids` (Set of String) Numeric IDs of schedules whose on-call members to page.
- `urgency` (String) `HIGH`, `MEDIUM` or `LOW`. Overrides the urgency the targets are paged with.
- `user_ids` (Set of String) Numeric IDs of members to page.

## Import

Import is supported using the following syntax:

The [`terraform import` command](https://developer.hashicorp.com/terraform/cli/commands/import) can be used, for example:

```shell
terraform import vibeoncall_alert_route.payments 42
```
