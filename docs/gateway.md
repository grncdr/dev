# Gateway

The gateway is a public-facing server that allows you to share your local development environment with others via stable HTTPS URLs. It accepts incoming requests and forwards them through secure tunnels to connected dev daemons.

## Use cases

- Share work-in-progress with teammates without deploying
- Test on mobile devices using a real URL
- Demo features to stakeholders from your local machine

## How it works

1. You run `dev gateway run` on a server with a public IP.
2. Team members run `dev share <label>` from their local machines to open tunnels
3. Requests to `<label>.example.com` are forwarded through the tunnel to the local daemon
4. The local daemon routes the request using the same proxy rules as local development

## Setting up the gateway

This is generally done once per organization.

The gateway requires configuration in `daemon.toml` (see `docs/config.md`):

```toml
[gateway]
dns_zone = "tunnels.example.com"      # Required: apex domain for tunnel URLs
acme_email = "admin@example.com"      # Required: email for Let's Encrypt
hostname = "tunnels.example.com"      # CNAME target for DNS records
dns_zone = "wip.example.com"              # Tunnel domains use this zone

[gateway.route53]
enabled = true
hosted_zone_id = "Z1234567890"        # AWS Route53 hosted zone
```

The above config configures the gateway to be reachable at `tunnels.example.com`, with tunnels domains being created at `<label>.wip.example.com`.

The gateway terminates TLS itself using certificates from Let's Encrypt. It should **not** be run behind a reverse proxy like nginx or Caddy—run it directly on a server with ports 80 and 443 available.

Initialize gateway state and print a bootstrap invite code:

```bash
dev gateway init
```

Start the gateway:

```bash
dev gateway run
```

## Setting up each agent

This is done once per team member.

Generate an invite code:

```bash
dev gateway invite
```

Use the bootstrap invite from `dev gateway init` for the first agent login; after that, any connected agent can run `dev gateway invite` to issue more invites.

Redeem the invite to get credentials:

```bash
dev gateway login <invite-code>
```

This generates a client certificate stored locally, enabling secure tunnel connections.

## Setting up each project

This is done for each project that should be shared through the gateway.

Configure the gateway server in the project `.dev.toml`:

```toml
[gateway]
url = "https://tunnels.example.com"
```

Configure the processes that should receive tunneled traffic with `gateway.expose` in `.dev.toml`:

```toml
[process.my-server]
command = "..."
port = "random"

[gateway]
url = "https://tunnels.example.com"

[gateway.expose.my-server]
mode = "reverse_proxy" # the default
debug_log = "/tmp/my-server-gateway-traffic.log"
```

Only listed processes accept gateway-tunneled traffic

- `mode = "reverse_proxy"`: standard reverse proxy headers, no host/cookie/body rewrites. Local processes receive the usual `X-Forwarded-*` headers (`X-Forwarded-Host`, `X-Forwarded-Proto`, `X-Forwarded-For`) so they can detect the public hostname and scheme.
- `mode = "rewrite"`: rewrites local apex hosts/cookies to the gateway public apex for browser-facing compatibility. Use this when your app cannot easily support both local `.localhost` hostnames and your public gateway DNS zone at the same time.
- `debug_log = "<path>"`: appends full HTTP request/response transcripts for traffic tunneled to the process.

To share your locally running server via the gateway:

```
dev share --label 'cool-feature'
```

The label is prepended to the gateway servers configured `dns_zone` ("wip.example.com" above) to make your service available at https://cool-feature.wip.example.com

## Server state

The gateway stores all persistent data under `<state_dir>/gateway/`. With the default state directory, this is `~/.local/state/dev/gateway/`.

Example file tree:

```
gateway/
├── acme/                        # ACME certificate storage (Let's Encrypt)
│   └── ...
├── pki/
│   ├── agent-ca.pem             # CA certificate for signing agent certs
│   └── agent-ca-key.pem         # CA private key
└── state/
    ├── leases.json              # Active tunnel registrations
    └── invites.json             # Pending invite codes
```

If running in a container or other ephemeral infrastructure, ensure that this directory is backed up or stored on a persistent filesystem.

## Client state

On local developer machines, the state for gateways is stored like this:

```
gateway/
└── agent-credentials/
    └── tunnels.example.com/
        ├── client-key.pem
        ├── client.pem
        └── ca.pem
```

If these certs are lost you will need to log in to the gateway again.

## Related documentation

- [Gateway design](design/gateway.md) - architecture and protocol details
- [Configuration reference](config.md) - full list of gateway config options
