# octogee-fleet

`octogee-fleet` is the operator command line for a fleet of [OpenClaw](https://github.com/openclaw/openclaw) gateways. With it, an operator can add, deploy, restart, park and retire gateways, place users on gateways and move them between gateways, and follow every change in the operations journal.

`octogee-fleet` is a client of the platform's Admin API and nothing else. It holds no database or cloud credentials, and it has no dependencies outside the Go standard library.

## Install

```bash
go install github.com/octogee-ai/octogee-fleet@latest
```

Or download the archive for your system from [Releases](https://github.com/octogee-ai/octogee-fleet/releases), check it against `checksums.txt`, and put the `octogee-fleet` binary inside it on your `PATH`. Releases cover macOS and Linux on amd64 and arm64.

This tool was called `fleet` before. If you have a configuration at `~/.config/fleet/config.json`, move it to `~/.config/octogee-fleet/config.json`, and set `OCTOGEE_FLEET_CONFIG` where you set `FLEET_CONFIG`. Sign in again afterwards.

## Build

```bash
go build -o octogee-fleet .
```

## Configure

`octogee-fleet` reads `$OCTOGEE_FLEET_CONFIG`, then `$XDG_CONFIG_HOME/octogee-fleet/config.json`, then `~/.config/octogee-fleet/config.json`.

```json
{
  "environments": {
    "staging": {
      "adminApiOrigin": "https://admin.staging.example.com",
      "imageRepository": "registry.example.com/fleet/gateway",
      "oidc": {
        "issuer": "https://accounts.google.com",
        "clientId": "your-client-id.apps.googleusercontent.com",
        "clientSecret": "your-client-secret",
        "scopes": ["openid", "email"]
      }
    }
  }
}
```

An origin or issuer must use `https`. Plain `http` is accepted only for a loopback address, so credentials never cross a network unencrypted.

## Sign in

Operators sign in with a Google account. `octogee-fleet` has no tokens of its own.

Set this up once for each platform:

1. In your Google Cloud project, create an OAuth client of the type **TVs and Limited Input devices**. That is Google's name for sign-in from a command line. Google asks you to fill in a consent screen the first time.
2. Put the client's ID and secret in the `oidc` block above. Google does not treat this secret as confidential for this client type.
3. Tell the platform the same client ID and which email addresses are operators. In the Helm chart these are `fleetIdentity.googleClientId` and `fleetIdentity.operatorEmails`.

Then sign in:

```bash
octogee-fleet --env=staging login
```

`octogee-fleet` prints a URL and a code, and you approve the sign-in in any browser. `octogee-fleet` keeps the session in `$XDG_STATE_HOME/octogee-fleet/<environment>.json` or `~/.local/state/octogee-fleet/<environment>.json`, readable only by you, and renews it when it expires. Other issuers that offer the OAuth device authorization grant can be added to the platform later. A session belongs to the issuer, client and Admin API origin it was made for, so changing any of them asks you to sign in again.

```bash
octogee-fleet --env=staging logout
```

The `oidc` block takes these fields:

| Field | Meaning |
| --- | --- |
| `issuer` | The issuer URL, exactly as the issuer's discovery document states it |
| `clientId` | The OAuth client registered for `octogee-fleet` |
| `clientSecret` | Only for an issuer that requires one from an installed application, such as Google |
| `scopes` | Replaces the default `openid email offline_access`. Google needs `["openid", "email"]`. |

Automation names a program instead. Replace `oidc` with `"tokenCommand": ["program", "arg"]`, and `octogee-fleet` runs it from the configuration file's directory and sends the OIDC ID token it prints.

## Use

```bash
octogee-fleet --env=staging gateway ls
```

```bash
octogee-fleet --env=staging user migrate ada --to=shared-b
```

```bash
octogee-fleet --env=staging user migrate ada --to=shared-b --apply
```

Mutating commands print the request and send nothing until you pass `--apply`. Run `octogee-fleet help` for the command list.

## Documentation

- [The command line](docs/octogee-fleet-cli.md)
- [The Admin API it calls](docs/admin-api.md)

## Test

```bash
go test ./...
```

The tests run the real command line against a recording Admin API and OIDC issuer over HTTP.
