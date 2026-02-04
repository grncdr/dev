# dev-mode

Multi-project, multi-worktree development with automatic HTTPS routing and port management.

## Motivation

A typical dev environment for web development usually involves a process manager (foreman, docker-compose, etc.) to run your application code and maybe a supporting service or two (databases, caches, etc.)

You can get by with localhost:3000 and a few static port assignments, but if you want to work on multiple features/bug-fixes etc in parallel, it can be annoying to switch branches and keep your database schemas in sync. Around this time you might think of using git worktrees to have multiple parallel checkouts, but then you need to introduce dynamic port numbers and keep track of that, either in your head or with config files and a reverse proxy. Making all of this reproducible across the development machines of a team becomes a full-time job in and of itself.

dev-mode aims to provide a superior experience by integrating these common components:

- A project & git worktree aware process supervisor to start/stop/restart your services.
- A local certificate authority (so you can have https:// URLs in dev).
- A reverse proxy that maps localhost subdomains to worktrees.
- A tunneling gateway (so you can easily share work in progress or test on mobile devices).

## I still don't get it, how does it compare to ...?

### Process & container managers

**Foreman, Overmind, process-compose, Docker Compose**

These tools run your processes but stop there. You still need to configure a reverse proxy separately, manage port assignments across worktrees, and set up tunneling with yet another tool. dev-mode integrates all of this—the reverse proxy routes to your processes automatically, and the gateway server is built in.

### Reverse proxies

**nginx, Caddy, Traefik**

Traditional reverse proxies require your backends to already be running. dev-mode starts processes on-demand when the first request arrives and can shut them down after idle periods. No need to manually start services before visiting the URL.

### Tunneling tools

**ngrok, localtunnel, Cloudflare Tunnel**

These are typically paid services or require external infrastructure. dev-mode's gateway is free, open-source, and trivially self-hostable. More importantly, it integrates tightly with your local dev-mode setup—tunneled requests benefit from the same on-demand startup and worktree routing as local requests.

## Install

```bash
go install ./cmd/dev-mode
```

## Documentation

- [Usage guide](docs/usage.md)
- [Configuration reference](docs/config.md)
- [Routing design](docs/design/routing.md)
- [Gateway design](docs/design/gateway.md)
- [Logging](docs/logging.md)
