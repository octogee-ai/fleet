# The octogee-fleet command line

The platform runs OpenClaw gateways for its users:

- A **shared gateway** hosts many users.
- An **isolated gateway** hosts one user, on its own VM. It is created for that user by `user migrate --to-isolated`, not by `gateway add`.

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
| Release | A numbered, immutable deployment of a build to one gateway. A gateway can roll back to an earlier release. |

## How every command behaves

- **`--env=<name>` is required.** Names come from the operator's configuration; none are built in.
- **The Admin API is the only boundary.** `octogee-fleet` needs no database, cluster or cloud credentials, and starts no process except an environment's `tokenCommand`.
- **Identity is OpenID Connect.** Every request carries the ID token from the operator's sign-in, or the one an environment's `tokenCommand` prints. `octogee-fleet` renews an expired session and never mints or verifies tokens; the Admin API verifies each one.
- **Reads come first, then at most one mutating request, or one dry run of it.** A token the Admin API rejects stops the command before any mutation.
- **Mechanism, not policy.** Users move one at a time with `user migrate`. `octogee-fleet` does not decide when to add or retire shared gateways, when to rebalance users, or when to wake or idle an isolated gateway, and it has no commands for automated or advanced recovery of a gateway stuck mid-move; those belong to whatever you run on top of the platform.
- **Dry run by default.** Without `--apply`, a mutating command prints the request it would send, then sends it to the Admin API marked `dryRun=true`. The Admin API runs the checks it makes before admitting the change and answers with what it would do, or with the refusal `--apply` would get. Nothing is changed, and a refusal exits non-zero. A bare `--apply` sends the request for real.
- **A dry run is not a guarantee.** It does not predict checks the Admin API makes while it applies a change, such as capacity or a conflicting change that lands first. It does not predict failures later in the operation either. `user unassign` and the `replace-vm` commands are checked only for a well-formed request.
- **`--offline`** prints the request without sending a dry run. The command's reads still run. It cannot be combined with `--apply`.
- **An Admin API that does not answer dry runs is never sent one.** Such a server would ignore the marker and apply the request, so `octogee-fleet` sends a dry run only after a read in the same command showed that the server supports it. Otherwise it prints the request and says that it was not checked.
- **`--idempotency-key=<key>`** becomes the request's `Idempotency-Key` header. A retried command with the same key changes nothing twice.
- **Applied mutations are journaled.** The command says what changed and prints the operation ids the Admin API returns, with the command to follow the first one. `gateway park`, `gateway resume`, `user assign` and `user unassign` return no id; the command says so and points at `ops ls`.
- **Break glass takes two flags.** Replacing a VM in a way that loses boot-disk data needs `--force --accept-boot-data-loss`; either flag alone is refused.
- **Refusals send no mutation and exit non-zero.** That covers an unknown command, an unknown or repeated flag, an unknown user or gateway, and a malformed commit sha. Flags are written `--name=value` or `--name`. A mistyped command or flag gets a suggestion.
- **Errors name the status and the server's code.** A `403` is reported as not permitted, with the Admin API's code, rather than as a rejected sign-in. Each error says what to try next.
- **Text for people, JSON for scripts.** At a terminal, text is coloured, states carry marks, and times are relative. Piped text is plain with the same columns. `NO_COLOR` or `--no-color` drops the colour; `FORCE_COLOR` or `CLICOLOR_FORCE` keeps it when piped. Every command that prints data takes `--format=json`: a read prints the Admin API's response with absolute times, and a mutation prints `{"dryRun", "checked", "request", "plans"}` for a dry run or `{"dryRun": false, "request", "response"}` once applied. A change already in place prints `{"unchanged": true, "reason"}`. JSON is never decorated, and warnings go to stderr.
- **Help is built in.** `octogee-fleet help`, `octogee-fleet help <noun>` and `octogee-fleet help <command>` or `<command> --help` print the commands, flags and examples. `show` is an alias of `inspect`.
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
| `gateway ls [--all]` | Lists gateways with status, the VM state of each isolated gateway, version, mounted users and last heartbeat. `--all` includes retired gateways. | `GET /gateways`, then `GET /gateways/<gateway>/details` for each isolated gateway that is not retired |
| `gateway inspect <gateway>` | Shows one gateway's state, release, VM, pods, last error, mounted users and recent operations | `GET /gateways?all=true`, `GET /gateways/<gateway>/details` |
| `gateway add --id=<id> [--sha=<full-sha>]` | Adds a shared gateway on an existing immutable build, or on the build the platform is running when `--sha` is omitted. The operator owns it: no automation replaces, drains or retires it. | `POST /gateways` |
| `gateway deploy <gateway> --sha=<full-sha>` | Deploys an existing immutable build to one gateway | `POST /gateways/<gateway>/deploy` |
| `gateway restart <gateway>` | Restarts the gateway | `POST /gateways/<gateway>/restart` |
| `gateway park <gateway>` | Pauses the gateway | `PATCH /gateways/<gateway>` `{desiredStatus: "paused"}` |
| `gateway resume <gateway>` | Resumes a parked gateway | `PATCH /gateways/<gateway>` `{desiredStatus: "running"}` |
| `gateway retire <gateway> [--retain-data] [--retain-service-account]` | Retires an empty gateway | `DELETE /gateways/<gateway>` |
| `gateway releases <gateway>` | Lists the gateway's releases | `GET /gateways/<gateway>/releases` |
| `gateway rollback <gateway> --release=<n>` | Rolls the gateway back to release `n` | `POST /gateways/<gateway>/releases/<n>/rollback` |
| `gateway replace-vm <gateway> [--force --accept-boot-data-loss]` | Replaces an isolated gateway's VM from the current image, keeping the verified home disk | `POST /gateways/<gateway>/vm/replace` |

A shared gateway stays on the build it was added on, and the Admin API refuses `gateway deploy` and `gateway rollback` on it. To move a shared gateway to a new build, add a shared gateway on that build with `gateway add --sha=`, move each user to it with `user migrate`, then retire the old gateway with `gateway retire`.

## User commands

| Command | What it does | Request |
| --- | --- | --- |
| `user ls [--gateway=<gateway>]` | Lists users and their placement, and reports drift in both directions | `GET /placement` |
| `user inspect <user>` | Shows one user's placement, provisioning and drift, and their isolated gateway with its VM state | `GET /placement`, `GET /gateways`, then `GET /gateways/<gateway>/details` for each isolated gateway the user owns |
| `user add <name> --email=<address>` | Creates a user and admits them. The platform places no user by itself: the operator places them with `user assign`, and a product built on top of the platform may place users itself. Refuses a taken name. | `POST /users` |
| `user issue-passwords <user>` | Sets new random chat and git passwords and prints them once. Run it after `user inspect` shows provisioning `complete`, and again if a password is lost. Refuses a user who has not finished provisioning. | `POST /users/<id>/one-time-passwords` |
| `user assign <user> --to=<gateway>` | Places a user who has no gateway. Refuses a parked gateway and another user's isolated gateway. | `POST /users/<id>/assign` |
| `user migrate <user> --to=<gateway> [--source-transfer=retained-pod]` | Moves a placed user to another gateway as a journaled migration | `POST /users/<id>/migrate` |
| `user migrate <user> --to-isolated=<sizing profile>` | Moves a placed user to an isolated gateway that does not exist yet. The Admin API creates it for the user at the sizing profile and queues the migration. Takes exactly one of `--to` and `--to-isolated`. Once the user has an isolated gateway, move them back to it with `--to=<gateway>`. | `POST /users/<id>/isolated-gateway` |
| `user unassign <user>` | Removes a user's placement | `POST /users/<id>/unassign` |
| `user replace-vm <user> [--force --accept-boot-data-loss]` | Replaces the VM of the user's isolated gateway | `POST /users/<id>/vm/replace` |
| `user delete <user> [--erase-now]` | Schedules the user's deletion; `--erase-now` erases at once | `POST /users/<id>/delete` |
| `user broker <user>` | Shows the user's model broker diagnostics | `GET /users/<id>/broker-diagnostics` |

## Platform commands

| Command | What it does | Request |
| --- | --- | --- |
| `ops ls [--status=] [--limit=]` | Lists operations and audits from the journal. An audit without a kind is named by what it did. | `GET /operations` |
| `ops inspect <operation>` | Shows one operation's fields, steps and failure reason. `ops show` is the same command. | `GET /operations/<operation>` |
| `builds ls [--service=] [--limit=]` | Lists builds with their tag, push time and digest | `GET /builds` |
| `release status` | Shows release convergence across the fleet | `GET /release-status` |
| `logs [--level=] [--service=] [--user=] [--migration=] [--gateway=] [--since=] [--no-follow] [--format=text\|json]` | Tails shared-gateway and VM-gateway logs together. With `--no-follow` it stops after two minutes if the Admin API has not finished, and says when older entries in the window were not read. | `GET /logs` |

## Shell commands

| Command | What it does | Request |
| --- | --- | --- |
| `completion bash\|fish\|zsh` | Prints a completion script for commands and flags. It needs no `--env`. | None |
