# The octogee-fleet command line

The platform runs OpenClaw gateways for its users:

- A **shared gateway** hosts many users.
- An **isolated gateway** hosts one user, on its own VM. It comes from that user's activation lease, not from `gateway add`.

## Terms

| Term | Meaning |
| --- | --- |
| Operator | The person who runs the platform. Every `octogee-fleet` request carries the operator's identity. |
| User | A person the platform serves. Each user is placed on one gateway and has an OpenClaw agent there. |
| Gateway | An OpenClaw gateway the platform runs: shared or isolated. |
| VM | The machine an isolated gateway runs on. Its home disk holds the user's state; its boot disk is replaceable. |
| Placement | Which gateway a user is assigned to. A gateway reports the users it has actually mounted; a mismatch between the two is drift. |
| Migration | A journaled move of a user from one gateway to another, carrying their channels, repositories and agent. |
| Operation | One entry in the operations journal. Every applied fleet mutation writes one. |
| Activation lease | A user's claim to start their isolated gateway's VM. It can be requested, cancelled, terminated or returned. |
| Release | A numbered, immutable deployment of a build to one gateway. A gateway can roll back to an earlier release. |

## How every command behaves

- **`--env=<name>` is required.** Names come from the operator's configuration; none are built in.
- **The Admin API is the only boundary.** `octogee-fleet` needs no database, cluster or cloud credentials, and starts no process except an environment's `tokenCommand`.
- **Identity is OpenID Connect.** Every request carries the ID token from the operator's sign-in, or the one an environment's `tokenCommand` prints. `octogee-fleet` renews an expired session and never mints or verifies tokens; the Admin API verifies each one.
- **Reads come first, then at most one mutating request.** A token the Admin API rejects stops the command before any mutation.
- **Dry run by default.** A mutating command prints the request it would send. A bare `--apply` sends it.
- **`--idempotency-key=<key>`** becomes the request's `Idempotency-Key` header. A retried command with the same key changes nothing twice.
- **Applied mutations are journaled.** The command prints the operation id the Admin API returns.
- **Break glass takes two flags.** Replacing a VM in a way that loses boot-disk data needs `--force --accept-boot-data-loss`; either flag alone is refused.
- **Refusals send no mutation and exit non-zero.** That covers an unknown command, an unknown or repeated flag, an unknown user or gateway, and a malformed commit sha. Flags are written `--name=value` or `--name`.
- **`<user>`** is a user's name or id. **`<gateway>`** is a gateway id. `gw` is short for `gateway`.

Requests below are relative to `/api/admin`.

## Sign-in commands

These talk to the environment's OIDC issuer, not the Admin API.

| Command | What it does | Request |
| --- | --- | --- |
| `login` | Signs in with the device authorization grant and keeps the session for this environment | The issuer's device and token endpoints |
| `logout` | Forgets this environment's session | None |

## Gateway commands

| Command | What it does | Request |
| --- | --- | --- |
| `gateway ls [--all]` | Lists gateways with status, version, mounted users and last heartbeat. `--all` includes retired gateways. | `GET /gateways` |
| `gateway inspect <gateway>` | Full diagnostic for one gateway | `GET /gateways`, `GET /gateways/<gateway>/details` |
| `gateway add --id= --image-tag=` | Registers a shared gateway. The image repository comes from the environment. | `POST /gateways` |
| `gateway deploy <gateway> --sha=<full-sha>` | Deploys an existing immutable build to one gateway | `POST /gateways/<gateway>/deploy` |
| `gateway restart <gateway>` | Restarts the gateway | `POST /gateways/<gateway>/restart` |
| `gateway park <gateway>` | Pauses the gateway | `PATCH /gateways/<gateway>` `{desiredStatus: "paused"}` |
| `gateway resume <gateway>` | Resumes a parked gateway | `PATCH /gateways/<gateway>` `{desiredStatus: "running"}` |
| `gateway retire <gateway> [--retain-data] [--retain-service-account]` | Retires an empty gateway | `DELETE /gateways/<gateway>` |
| `gateway releases <gateway>` | Lists the gateway's releases | `GET /gateways/<gateway>/releases` |
| `gateway rollback <gateway> --release=<n>` | Rolls the gateway back to release `n` | `POST /gateways/<gateway>/releases/<n>/rollback` |
| `gateway recover-source <gateway> --sha= [--supersede-recovery=<id>]` | Repairs a shared gateway's source after a failed database export, keeping its pod storage | `POST /gateways/<gateway>/recover-source` |
| `gateway recover-restore <gateway> --sha= --operation=<id> [--reauthorize-attempt=<id>] [--supersede-recovery=<id>]` | Upgrades a fenced restore target without replacing its storage | `POST /gateways/<gateway>/recover-restore` |
| `gateway replace-vm <gateway> [--force --accept-boot-data-loss]` | Replaces an isolated gateway's VM from the current image, keeping the verified home disk | `POST /gateways/<gateway>/vm/replace` |
| `gateway move-users <source> --to=<target> [--users=<a,b>]` | Moves the listed users, or all of them, through journaled migrations | `POST /gateway-migrations/bulk` |

## User commands

| Command | What it does | Request |
| --- | --- | --- |
| `user ls [--gateway=<gateway>]` | Lists users and their placement, and reports drift in both directions | `GET /placement` |
| `user inspect <user>` | Full diagnostic for one user | `GET /placement` |
| `user add <name> --email=<address>` | Creates a user and admits them. The platform places them on a gateway and provisions chat and git. Refuses a taken name. | `POST /users` |
| `user issue-passwords <user>` | Sets new random chat and git passwords and prints them once. Run it after `user inspect` shows provisioning `complete`, and again if a password is lost. Refuses a user who has not finished provisioning. | `POST /users/<id>/one-time-passwords` |
| `user assign <user> --to=<gateway>` | Places a user who has no gateway. Refuses a parked gateway and another user's isolated gateway. | `POST /users/<id>/assign` |
| `user migrate <user> --to=<gateway> [--source-transfer=retained-pod]` | Moves a placed user to another gateway as a journaled migration | `POST /users/<id>/migrate` |
| `user unassign <user>` | Removes a user's placement | `POST /users/<id>/unassign` |
| `user replace-vm <user> [--force --accept-boot-data-loss]` | Replaces the VM of the user's isolated gateway | `POST /users/<id>/vm/replace` |
| `user lease request\|cancel\|terminate\|return <user> [--lease=<id> --generation=<n>]` | Manages the user's activation lease | `POST /users/<id>/activation-lease[/cancel\|/terminate\|/return]` |
| `user lease retry-return <user> --operation=<id>` | Retries a failed lease return | `POST /users/<id>/activation-lease/return/retry` |
| `user delete <user> [--erase-now]` | Schedules the user's deletion; `--erase-now` erases at once | `POST /users/<id>/delete` |
| `user broker <user>` | Shows the user's model broker diagnostics | `GET /users/<id>/broker-diagnostics` |

## Platform commands

| Command | What it does | Request |
| --- | --- | --- |
| `ops ls [--status=] [--limit=]` | Lists operations from the journal | `GET /operations` |
| `ops inspect <operation>` | Shows one operation's steps and failure reason | `GET /operations/<operation>` |
| `builds ls [--service=] [--limit=]` | Lists builds | `GET /builds` |
| `release status` | Shows release convergence across the fleet | `GET /release-status` |
| `pool show` | Shows the shared-gateway placement pool | `GET /placement` |
| `pool autoscaling [--enabled=true\|false] [--users-per-gateway=<n>]` | Sets the shared-gateway pool's autoscaling, keeping any setting not named | `PATCH /placement/autoscaling` |
| `logs [--level=] [--service=] [--user=] [--migration=] [--gateway=] [--since=] [--no-follow] [--format=text\|json]` | Tails shared-gateway and VM-gateway logs together | `GET /logs` |
