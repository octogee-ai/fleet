# The Admin API contract

`octogee-fleet` depends on the platform only through these responses. Requests are listed in [the command reference](octogee-fleet-cli.md). All are under `/api/admin`, carry `Authorization: Bearer <OIDC ID token>`, and return JSON. Fields not listed here are ignored.

## Identity

The bearer is an ID token from an OpenID Connect issuer the platform trusts, which today is Google. The Admin API checks its signature against the issuer's published keys, and its issuer, audience and expiry. A verified email on the platform's operator list holds the operator role. There are no other operator credentials. `octogee-fleet` reads only the token's expiry, to decide when to renew it.

## Errors

A failed request returns a non-2xx status with `{"error": "<message>"}`. `401` means the token is missing, expired or not from a trusted issuer. `403` means it lacks the operator role.

## Mutations

A journaled mutation returns `{"operationId": "<id>"}`.

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
    "lastHeartbeatAt": "2026-11-10T00:00:00Z",
    "ownerId": null
  }]
}
```

`gatewayClass` is `shared` or `isolated`. An isolated gateway names its `ownerId`.

## `GET /placement`

```json
{
  "config": {
    "defaultGatewayId": "shared-a",
    "sharedGatewayIds": ["shared-a"],
    "sharedGatewayAutoscaling": { "enabled": true, "usersPerGateway": 40 }
  },
  "gateways": [{
    "gatewayId": "shared-a",
    "gatewayClass": "shared",
    "desiredStatus": "running",
    "assignedCount": 1,
    "mountedCount": 1,
    "driftCount": 0
  }],
  "users": [{
    "id": "u-ada",
    "name": "ada",
    "assignedGatewayId": "shared-a",
    "mountedGatewayId": "shared-a",
    "provisioningStatus": "complete",
    "driftState": "healthy"
  }]
}
```

`driftState` is `healthy`, `in-flight`, `assigned-not-mounted`, `mounted-orphan`, `pointer-drift` or `missing-gateway`.

## `GET /gateways/<gateway>/releases`

```json
{ "releases": [{ "release": 3, "imageTag": "v42", "createdAt": "2026-11-10T00:00:00Z", "createdBy": "operator", "live": true, "desired": true }] }
```

## `GET /operations`

```json
{ "operations": [{ "id": "op-1", "kind": "deploy", "status": "running", "createdBy": "operator" }] }
```

## `GET /builds`

`octogee-fleet` prints the `builds` array as returned.

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
