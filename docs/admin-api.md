# The Admin API contract

`octogee-fleet` depends on the platform only through these responses. Requests are listed in [the command reference](octogee-fleet-cli.md). All are under `/api/admin`, carry `Authorization: Bearer <OIDC ID token>`, and return JSON. Fields not listed here are ignored.

## Identity

The bearer is an ID token from an OpenID Connect issuer the platform trusts, which today is Google. The Admin API checks its signature against the issuer's published keys, and its issuer, audience and expiry. A verified email on the platform's operator list holds the operator role. There are no other operator credentials. `octogee-fleet` reads only the token's expiry, to decide when to renew it.

## Errors

A failed request returns a non-2xx status with `{"error": "<message>"}`, and a machine-readable `code` where the route has one. `401` means the token is missing, expired or not from a trusted issuer. `403` means the identity may not do this, such as one without the operator role. `octogee-fleet` prints the status and `code` with the message.

## Mutations

A journaled mutation returns `{"operationId": "<id>"}`, and a mutation that starts more than one operation names each in a field ending in `OperationId`, such as `erasureOperationId`. `PATCH /gateways/<gateway>`, `POST /users/<id>/assign` and `POST /users/<id>/unassign` are journaled as audits but return no id.

## Dry runs

A mutating request with the query parameter `dryRun=true` is checked and not applied. The Admin API runs every check it makes before admitting the change, then returns `200`:

```json
{
  "dryRun": true,
  "plans": [{
    "what": "restart gateway",
    "target": { "gatewayId": "shared-a" },
    "preActionStatus": { "desiredStatus": "running", "desiredRelease": 3 },
    "forced": false
  }]
}
```

`what` names the change, `target` is what it acts on, and `preActionStatus` is the state it would start from. `forced` is true for a break-glass request. A plan also carries `why`, `kind` and `mutation` as the operations journal would record them.

A dry run writes nothing and leaves its `Idempotency-Key` unused, so `octogee-fleet` sends none. A request the Admin API would refuse gets the status and error of the real request. `dryRun` must be `true` or `false`; any other value returns `400`.

Every response carries the header `Admin-Api-Capabilities: dry-run`. An Admin API without it ignores `dryRun` and applies the request, so `octogee-fleet` sends a dry run only after it has seen the header. A `2xx` answer without `"dryRun": true` is reported as a change that may have been applied.

A dry run does not predict checks made while a change is applied, an idempotency key that was already used, or failures later in the operation.

## `GET /gateways`

`?all=true` includes retired gateways.

```json
{
  "gateways": [{
    "gatewayId": "shared-a",
    "gatewayClass": "shared",
    "desiredStatus": "running",
    "status": "ready",
    "imageTag": "v42",
    "openclawVersion": "2026.11.1",
    "mountedUsers": ["ada"],
    "lastHeartbeatAt": "2026-11-10T00:00:00.000Z",
    "ownerId": null
  }]
}
```

`gatewayClass` is `shared` or `isolated`. An isolated gateway names its `ownerId`. A summary also carries `lastError` (`{"slug", "message", "at"}`) and `podDiagnostics` when the platform has them:

```json
{
  "status": "ready", "totalPods": 1, "readyPods": 1,
  "pods": [{ "name": "<pod>", "phase": "Running", "readyContainers": 2, "totalContainers": 2, "restarts": 0, "reason": null }]
}
```

`octogee-fleet` counts a pod as ready when its `phase` is `Running` and every container is ready.

## `GET /gateways/<gateway>/details`

```json
{
  "mountedUsers": [{ "uid": "u-ada", "username": "ada", "email": "ada@example.com", "lastActive": "2026-11-10T00:00:00Z" }],
  "recentOperations": [{ "id": "op-1", "kind": "restart-gateway", "status": "completed", "createdBy": "operator", "createdAt": 1794268800000 }],
  "control": {
    "desiredRelease": 3,
    "liveRelease": 3,
    "sizingTier": "standard",
    "vm": { "instanceName": "<name>", "zone": "<zone>", "status": "SUSPENDED", "stopReason": "idle", "provisioningModel": "SPOT", "startedAt": 1794268800000, "updatedAt": 1794268800000 }
  }
}
```

`control.vm` is `null` for a gateway without a VM. `status` is the VM's state as the cloud reports it, such as `RUNNING`, `SUSPENDED` or `TERMINATED`, and `stopReason` is `idle`, `preempted`, `operator` or `null`. `GET /gateways` does not carry the VM, so `gateway ls` reads the details of each isolated gateway. `recentOperations` lists operations journaled against the gateway; audits of operator changes such as a restart or a park are not among them.

## `POST /gateways`

Takes `{"gatewayId": "shared-<lowercase letters or digits>", "sourceSha": "<full commit sha>"}`. `sourceSha` is optional; without it the gateway runs the build the platform is running. A body carrying `imageRepository` or `imageTag` returns `400`. Returns `202`:

```json
{ "gatewayId": "shared-c", "sourceSha": "<full commit sha>", "release": 1, "imageTag": "<tag>", "gatewayEdgeImageTag": "<tag>", "operationId": "<id>" }
```

`sourceSha` is `null` when the request named none. A dry run answers with one plan whose `mutation` has kind `add-gateway` and carries `gatewayId`, `build` (`{"sourceSha", "manifestDigest"}` or `"platform"`), `imageTag` and `gatewayEdgeImageTag`. Refusals are the same on a dry run and when applied. `400` means a malformed `sourceSha`, a gateway id the platform refuses, or no `sourceSha` while the platform's running build is unknown. `404` means no build manifest exists for `sourceSha`. `409` means the id is in use or the build's manifest does not match. `503` means build manifests are unavailable.

A shared gateway added this way belongs to the operator: no automation replaces, drains or retires it, and it stays on its build. `POST /gateways/<gateway>/deploy` and `POST /gateways/<gateway>/releases/<n>/rollback` on a shared gateway return `409`.

## `GET /placement`

```json
{
  "users": [{
    "id": "u-ada",
    "name": "ada",
    "email": "ada@example.com",
    "policyTargetGatewayId": "shared-a",
    "assignedGatewayId": "shared-a",
    "mountedGatewayId": "shared-a",
    "provisioningStatus": "complete",
    "driftState": "healthy",
    "latestOperation": { "id": "op-1", "kind": "migrate", "status": "completed" }
  }]
}
```

`driftState` is `healthy`, `in-flight`, `assigned-not-mounted`, `mounted-orphan`, `pointer-drift` or `missing-gateway`. `pointer-drift` means the user is placed on a gateway other than `policyTargetGatewayId`, which `octogee-fleet` calls the placement target. `missing-gateway` means the gateway the user is placed on, or the placement target, is retired or unknown.

## `GET /gateways/<gateway>/releases`

```json
{ "releases": [{ "release": 3, "imageTag": "v42", "createdAt": "2026-11-10T00:00:00Z", "createdBy": "operator", "live": true, "desired": true }] }
```

## `GET /operations`

```json
{
  "operations": [
    { "id": "op-1", "kind": "deploy", "status": "running", "createdBy": "operator", "createdAt": 1794268800000 },
    { "id": "audit-1", "recordType": "audit", "what": "update gateway desired configuration", "status": "completed", "createdBy": "operator", "createdAt": 1794268800000 }
  ]
}
```

An audit records an operator change. Its `kind` is the mutation's kind and may be missing; `what` always says what it did.

## `GET /operations/<operation>`

```json
{
  "operation": {
    "id": "op-1", "kind": "migrate", "status": "failed", "createdBy": "operator", "createdAt": 1794268800000,
    "error": "<message>",
    "steps": [{ "name": "quiesce-source", "status": "failed", "error": "<message>" }]
  }
}
```

## `GET /builds`

```json
{ "service": "gateway", "builds": [{ "tag": "<tag>", "pushedAt": "2026-11-10T00:00:00Z", "digest": "sha256:<hex>" }], "generatedAt": "2026-11-10T00:00:00Z" }
```

## `GET /release-status`

```json
{
  "summary": { "status": "converged", "converged": 1, "serving": 1, "blockers": [] },
  "gateways": [{ "gatewayId": "shared-a", "desiredRelease": 3, "liveRelease": 3, "runtimeRelease": 3, "observation": "converged" }]
}
```

## `GET /logs`

A stream of newline-delimited JSON, open until the client disconnects unless `follow=false`. It accepts `level`, `service`, `user`, `migration`, `gateway`, `since` and `follow`.

```json
{ "timestamp": "2026-11-10T00:00:00Z", "severity": "WARNING", "service": "api", "message": "..." }
```

With `follow=false`, the Admin API reads the window's history before it writes any of it. When it stops before the start of the window, its last entry carries `"details": {"truncated": true}`.

## `POST /users`

Takes `{"name": "<name>", "email": "<address>"}`. Returns `201 {"userId": "<id>", "operationId": "<id>"}`. `400` means the name or email is malformed. `409` means either is taken. `501` means this platform has no user account backend.

## `POST /users/<id>/one-time-passwords`

Takes no body. Sets new random passwords on chat and git and returns them once. They are not stored or journaled, and the response carries `Cache-Control: no-store`.

```json
{
  "operationId": "<id>",
  "chat": { "username": "<name>", "password": "<password>" },
  "git": { "username": "<name>", "password": "<password>" }
}
```

`404` means the user does not exist. `409` means the user has not finished provisioning or the account is not active. Any other failure returns no passwords; calling it again replaces both.

## `POST /users/<id>/isolated-gateway`

Takes `{"sizingProfile": "<name>"}`. Creates the user's isolated gateway at that sizing profile and queues a migration of the user to it. Returns `202`:

```json
{ "uid": "u-ada", "gatewayId": "<id>", "operationId": "<id>", "sizingProfile": "<name>", "fromGatewayId": "shared-a", "managedBy": "operator" }
```

A dry run answers with one plan whose `mutation` carries `uid`, `sizingProfile` and `fromGatewayId`. Refusals carry `{"code": "<code>", "error": "<message>"}`, on a dry run as well as when applied. `400` means the sizing profile is unknown. `404` with `user-not-found` means the user does not exist. `409` with `user-has-isolated-gateway` means the user already has one, named by `gatewayId`; `isolated-gateway-retiring`, `user-not-provisioned` and `user-not-active` are also `409`.
