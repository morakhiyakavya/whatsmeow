# Azure Spot VM recovery

The Render process can keep the existing Azure importer VM running. This is an
external recovery controller: it starts immediately and polls Azure every five
minutes. No new service, database migration or dependency is required.

## Configuration

Enable these settings on Render only:

```dotenv
AZURE_VM_RECOVERY_ENABLED=true
AZURE_SUBSCRIPTION_ID=<subscription UUID>
AZURE_RESOURCE_GROUP=venweb-api_group
AZURE_VM_NAME=venweb-api
AZURE_TENANT_ID=<directory UUID>
AZURE_CLIENT_ID=<application UUID>
AZURE_CLIENT_SECRET=<secret value>
```

The existing names `directory_id`, `application_id` and `secret_value` are accepted
as fallbacks. Canonical `AZURE_*` credential names take precedence. `object_id`
and `secret_id` are not used for authentication; the secret value is required.
Credentials remain private environment settings and never belong in Git, Android,
HTTP responses or logs. A missing/false enable flag disables recovery. An invalid
enabled configuration rejects startup with a credential-free error.

The service principal needs VM read, instance-view read and start permissions:

```text
Microsoft.Compute/virtualMachines/read
Microsoft.Compute/virtualMachines/instanceView/read
Microsoft.Compute/virtualMachines/start/action
```

Scope its role to this VM rather than granting subscription-wide administration.
The controller does not assign roles or change existing permissions. Azure login
and management URLs are fixed; credential-bearing redirects are not followed.

## Recovery behaviour

Each bounded check obtains an Azure management token using the existing OAuth2
library and queries the VM instance view. A stopped or deallocated VM receives a
start request. Running, starting, stopping and deallocating states are left alone.
Unknown states, authorization errors and malformed responses cause no mutation.
Accepted starts mean a request was accepted, not that startup completed. The next
poll verifies the resulting power state. Capacity failures are retried on later
five-minute polls. Checks run sequentially and stop with the application context.

`GET /health` keeps its database/liveness behaviour and adds the last cached
`azure_vm` state, action, check time and redacted error when enabled. It performs
no Azure calls or VM mutations. An unavailable Azure VM does not make Render
unhealthy or trigger a Render restart. HTTP failure alone cannot establish that
a VM is powered off; a running VM with an application/network failure is not
rebooted by this controller.

The existing Sentry monitor checks Render health every 60 seconds. The loop only
runs while Render is awake and available. Keep external monitoring active; Render
Free can sleep or be suspended, so this is not an availability guarantee.
There is no new message/notification destination in this increment; controller
failures use the existing redacted logs and Sentry error capture.

Recovery also restarts a VM that an operator manually stops or deallocates.
Disable `AZURE_VM_RECOVERY_ENABLED` and deploy/restart Render before planned VM
maintenance. Re-enable it afterward. Leave it disabled on the Spot host itself.

Spot capacity may remain unavailable indefinitely; retrying cannot prevent an
eviction or guarantee 24/7 operation. Check application HTTPS health separately
after the VM is running. See Microsoft's [instance-view API](https://learn.microsoft.com/en-us/rest/api/compute/virtual-machines/instance-view),
[start API](https://learn.microsoft.com/en-us/rest/api/compute/virtual-machines/start)
and [Spot recovery guidance](https://learn.microsoft.com/en-us/azure/architecture/guide/spot/spot-eviction).
