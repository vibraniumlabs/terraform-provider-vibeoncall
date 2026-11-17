# Complete example

A team's on-call setup, end to end:

- A team with admins and members.
- A schedule where the rotation participants, admins or members, take turns weekly, handing off on Mondays at 09:00.
- A two-tier escalation policy: whoever is on call first, then every admin. It is the team's default policy.
- A service owned by the team.
- An alert route that pages the escalation policy for SEV0 and SEV1 alerts, and whoever is on call for everything else.
- An alert source that a monitoring tool sends alerts to, feeding that route.

Every user must already exist in Vibe OnCall, usually provisioned through SCIM from your identity provider; the example looks them up by email.

## Run it

1. Create an API key in Vibe OnCall that can manage teams, schedules, escalation policies, services, alert routing and alert sources, and export it:

   ```shell
   export VIBEONCALL_API_KEY=...
   ```

2. Copy `terraform.tfvars.example` to `terraform.tfvars` and fill in your team, service, users and a start date.

3. Apply, then plan again. The second plan shows no changes.

   ```shell
   terraform init
   terraform apply
   terraform plan
   ```

Point your monitoring tool at the alert source's webhook URL:

```shell
terraform output -raw alert_source_webhook_url
```

`terraform destroy` removes everything the example created. The users themselves are not deleted.
