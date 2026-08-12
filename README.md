# ocpgate

A single, audited entrypoint for accessing OpenShift (OCP) clusters.

`ocpgate` reads your cluster list from a GitOps-managed GitLab registry,
authenticates you against the selected cluster's LDAP-backed OAuth endpoint, and
drops you into a shell with a temporary kubeconfig that is deleted when you exit.
Every access is emitted as a structured JSON audit event.

**No kubeconfig, password, or token is ever written to a permanent location.**

---

## The problem

- Kubeconfigs live scattered across per-installer machines — no central truth source.
- Engineers SSH into installer machines to run cluster commands: slow, insecure, unaudited.
- No record of who accessed which cluster, when, and from where.

`ocpgate` closes that gap without standing up new infrastructure: it reuses LDAP
for identity, GitLab for the cluster registry, and the existing Fluentd →
OpenSearch pipeline for audit.

> **Status: interim solution.** This is a deliberate bridge toward a full
> HashiCorp Vault + Boundary adoption. See [Migration path](#migration-path).

---

## How it works

```
 ┌──────────┐   1. pick cluster     ┌─────────────────┐
 │ engineer │ ────────────────────► │ cluster registry│  (GitLab, GitOps)
 └────┬─────┘                       └─────────────────┘
      │ 2. LDAP credentials (masked, never stored)
      ▼
 ┌─────────────────┐   3. OAuth challenge   ┌──────────────────┐
 │     ocpgate     │ ─────────────────────► │ OCP OAuth (LDAP) │
 └────┬────────────┘ ◄───────────────────── └──────────────────┘
      │                 4. short-lived Bearer token
      │
      │ 5. write temp kubeconfig → ~/.cache/ocpgate/sessions/<id>/
      │ 6. exec $SHELL with KUBECONFIG set
      │ 7. on exit: delete kubeconfig
      ▼
 ┌─────────────────┐
 │  audit (stdout) │ ──► Fluentd ──► OpenSearch (ocpgate-audit-*)
 └─────────────────┘
```

The token is short-lived and issued by the cluster itself. Even if cleanup never
runs, nothing durable is left behind.

---

## Install

Requires **Go 1.24+**.

```bash
git clone https://github.com/oziie/ocpgate.git
cd ocpgate
make build          # -> dist/ocpgate
# or
make install        # -> $GOBIN/ocpgate
```

---

## Configuration

`ocpgate` reads `~/.config/ocpgate/config.yaml` (override with `--config`).
A missing file is not an error — defaults apply and env vars layer on top.

```yaml
registry:
  gitlab_url: https://gitlab.example.com
  project_id: "123"
  branch: main
  token_env: OCPGATE_GITLAB_TOKEN   # token read from env, never stored in config
  cache_path: ~/.cache/ocpgate/clusters.json
  sync_on_start: true

audit:
  enabled: true
  writer: stdout                    # stdout only in v1

tui:
  default_namespace: default
  show_environment_badge: true
```

Export your GitLab read token:

```bash
export OCPGATE_GITLAB_TOKEN=glpat-xxxxxxxxxxxx
```

### Cluster registry

Clusters are defined as one YAML file each in the `clusters/` directory of the
registry repo:

```yaml
name: prod-cluster-1
api_endpoint: https://api.prod-cluster-1.example.com:6443
environment: production          # production | test
region: eu-west
ldap_realm: PROD
active: true                     # false = visible, but auth is refused
```

Names must be unique across all files, and `api_endpoint` must be an HTTPS URL
with an explicit port. Adding a cluster is a merge request — which is also the
change audit trail.

---

## Usage

```bash
ocpgate                                   # launch the TUI
ocpgate clusters list                     # cluster table (served from local cache)
ocpgate clusters list -e production       # filter by environment
ocpgate clusters sync                     # force a sync from GitLab

ocpgate connect prod-cluster-1            # authenticate and open a session
ocpgate connect prod-cluster-1 -u john.doe -n team-a

ocpgate connect prod-cluster-1 -- oc get pods   # run one command, then clean up

ocpgate sessions prune                    # remove leftovers from crashed runs
ocpgate version
```

Global flags:

| Flag | Description |
|---|---|
| `--config` | Path to config file (default `~/.config/ocpgate/config.yaml`) |
| `--insecure-skip-tls-verify` | Skip verification of the cluster's certificate chain (mirrors `oc login`). The generated kubeconfig records this too, so `kubectl` behaves the way `ocpgate` just did. |

### The TUI

Running `ocpgate` with no subcommand opens the interactive interface:

```
┌──────────────────────────────────────────────────────────────────┐
│  clusters                                                        │
├──────────────────────────────────────────────────────────────────┤
│  > ● prod-cluster-1     PRODUCTION    eu-west                    │
│    ● prod-cluster-2     PRODUCTION    eu-central                 │
│    ○ test-cluster-1     TEST          eu-west                    │
│    ○ old-cluster        INACTIVE      eu-west   (auth disabled)  │
│                                                                  │
│  ↑/k up · ↓/j down · / filter · e cycle environment · ? more     │
├──────────────────────────────────────────────────────────────────┤
│  user: —   cluster: —   namespace: —   token: —                  │
└──────────────────────────────────────────────────────────────────┘
```

It walks the same four steps as the CLI — cluster → credentials →
namespace → active session — with the status bar filling in as you go.

| Key | Action |
|---|---|
| `↑`/`k`, `↓`/`j` | Navigate |
| `/` | Search clusters (name, environment, or region) |
| `e` | Cycle the environment filter: all → production → test |
| `enter` | Select; in the session view, open a shell |
| `esc` | Go back (discards any token already issued) |
| `q` | End the session (session view) |
| `ctrl+c` | Quit |

The interface renders to **stderr**, so `ocpgate 1>>audit.log` captures the
audit stream while the TUI is on screen. With no terminal attached — a pipe
or a CI job — it prints help instead.

In the active-session view, `enter` suspends the TUI and hands you a shell
with `KUBECONFIG` already set; exiting the shell returns you to the
countdown. `q` ends the session and deletes the kubeconfig.

Namespace selection has a fallback: ordinary LDAP users on OCP usually
cannot list namespaces cluster-wide, so if the lookup is refused you get a
text field to type one instead. That is normal, not an error.

### A session

```console
$ ocpgate connect prod-cluster-1
Connecting to prod-cluster-1 (production)
Username [ozane]: john.doe
Password:

  cluster:   prod-cluster-1 (production)
  user:      john.doe
  namespace: team-a
  token:     expires in 24h0m0s (Sun, 02 Aug 2026 15:32:01 CEST)
  session:   550e8400-e29b-41d4-a716-446655440000

Type `exit` to end the session and delete the temporary kubeconfig.

$ oc get pods            # KUBECONFIG is already pointed at this session
$ exit
Session 550e8400-e29b-41d4-a716-446655440000 ended; temporary kubeconfig removed.
```

Inside the session, `KUBECONFIG`, `OCPGATE_SESSION_ID`, and `OCPGATE_CLUSTER` are
set. Any inherited `KUBECONFIG` is dropped, so there is no ambiguity about which
cluster you are talking to.

### Output convention

- **stdout** — audit JSON, one object per line, for Fluentd. Also the cluster
  table from `clusters list`, since that is what a user piping the command wants.
- **stderr** — all human-facing status, prompts, and warnings.

So `ocpgate connect prod-cluster-1 1>>audit.log` keeps a clean audit stream while
you still see prompts and the session banner.

### Degraded mode

If GitLab is unreachable or `OCPGATE_GITLAB_TOKEN` is unset, `clusters list` and
`connect` fall back to the local cache with a warning on stderr — an outage
should not stop you from reaching a cluster you already know about. An explicit
`clusters sync` still fails loudly.

---

## Audit events

One JSON object per line on stdout, collected by Fluentd into the
`ocpgate-audit-YYYY.MM.DD` index.

Event types: `auth_attempt`, `auth_failure`, `session_start`, `session_end`,
`registry_sync`, `token_expired`.

```json
{
  "@timestamp": "2026-05-02T14:32:01Z",
  "tool": "ocpgate",
  "version": "1.0.0",
  "event_type": "session_start",
  "user": { "username": "john.doe" },
  "cluster": {
    "name": "prod-cluster-1",
    "environment": "production",
    "api_endpoint": "https://api.prod-cluster-1.example.com:6443"
  },
  "session": {
    "id": "550e8400-e29b-41d4-a716-446655440000",
    "token_expiry": "2026-05-02T15:32:01Z"
  },
  "source": { "hostname": "johns-macbook", "ip": "10.10.1.45" },
  "outcome": "success"
}
```

Apply `deployments/opensearch/index-template.json` before the first log lands, so
`event_type`, `outcome`, `user.username`, `cluster.name`, `cluster.environment`,
and `session.id` are mapped as `keyword` and stay filterable.

---

## Modules

| Module | Responsibility |
|---|---|
| [`pkg/config`](pkg/config/) | Loads `config.yaml` via viper, layers env vars and defaults. Resolves the GitLab token from the environment — never from the config file. |
| [`pkg/version`](pkg/version/) | Build metadata (version, commit, date) injected via `ldflags`. |
| [`internal/registry`](internal/registry/) | Syncs cluster YAML from the GitLab repo, validates the schema, and caches to local disk. Reads are served from an in-memory snapshot, so `list`/`get` never block on the network. |
| [`internal/auth`](internal/auth/) | OCP OAuth challenge flow: discovers the OAuth server, exchanges LDAP credentials for a short-lived Bearer token. Credentials and tokens carry redacting `String()` methods so a stray `%v` cannot leak them. |
| [`internal/session`](internal/session/) | Writes a single-context kubeconfig (0600) into a per-session directory (0700) and removes it on exit. Cleanup is idempotent and refuses any path outside its own base directory. |
| [`internal/audit`](internal/audit/) | Builds and emits audit events as newline-delimited JSON. |
| [`internal/ocp`](internal/ocp/) | Direct cluster API queries that are neither auth nor session lifecycle — currently the namespace lookup behind the TUI selector. |
| [`internal/tui`](internal/tui/) | Bubble Tea interface — cluster list, credential prompt, namespace selector, and an active-session view with a token countdown. Renders to stderr so stdout stays the audit stream. |
| [`cmd/ocpgate`](cmd/ocpgate/) | Cobra CLI wiring the modules together. |

### Auth flow detail

```
GET  <api>/.well-known/oauth-authorization-server      # discovery
GET  <authorization_endpoint>?response_type=token&client_id=openshift-challenging-client
     Authorization: Basic base64(user:pass)
     X-CSRF-Token: 1
→    302, token in the Location fragment
```

`openshift-challenging-client` is OCP's built-in OAuth client that answers a
Basic-auth challenge with a Bearer token — the same flow `oc login` uses. The
`X-CSRF-Token` header is required: without it, OCP serves the HTML login page
instead of challenging. The HTTP client deliberately does **not** follow the
redirect, since the token lives in the fragment and the redirect target must
never be fetched.

On a real cluster the OAuth route (`oauth-openshift.apps.<domain>`) is a
different host from the API, which is why discovery comes first; it falls back to
`<api>/oauth/authorize` when the well-known endpoint is unavailable.

---

## Tech stack

| Concern | Choice |
|---|---|
| Language | Go 1.24+ (single binary, no runtime deps) |
| CLI | [cobra](https://github.com/spf13/cobra) |
| Config | [viper](https://github.com/spf13/viper) |
| TUI | [Bubble Tea](https://github.com/charmbracelet/bubbletea) + Bubbles + Lip Gloss |
| GitLab client | [go-gitlab](https://github.com/xanzy/go-gitlab) |
| Kubeconfig | [client-go](https://github.com/kubernetes/client-go) `clientcmd` |
| Logging | [zerolog](https://github.com/rs/zerolog) (structured JSON) |
| Testing | stdlib + [testify](https://github.com/stretchr/testify) |

`client-go` is pinned to v0.31.x: later releases raise the required Go version
to 1.26.

---

## Key design decisions

Full ADRs live in [`docs/decisions/`](docs/decisions/).

| Decision | Choice | Reason |
|---|---|---|
| Auth | Stateless — token per session, never stored | LDAP already owns identity; nothing durable to steal |
| Cluster registry | GitLab repo (GitOps) | No central infra, MR-based governance, built-in change audit |
| Kubeconfig | Generated per session, deleted on exit | No static secrets, short-lived tokens only |
| Audit destination | stdout → Fluentd → OpenSearch | Reuses the existing log pipeline, zero new infra |
| Mode | Passthrough, not proxy | 80% of the audit value at 20% of the complexity; proxy is v2 |

---

## Development

```bash
make check     # gofmt + go vet + go test
make test      # tests only
make cover     # coverage report in the browser
make fmt       # format
make build     # dist/ocpgate
```

The CLI has an end-to-end test that drives `connect` against a fake OCP OAuth
server and asserts the session directory is deleted afterwards — no cluster
required.

---

## Roadmap

- [x] Config, version, registry, auth, session, audit modules
- [x] Non-interactive CLI: `ocpgate connect <cluster>` end to end
- [x] Bubble Tea TUI — searchable cluster list, namespace selector, status bar
- [ ] Retry logic on transient GitLab / OAuth failures
- [ ] Token-expiry warning during long-lived sessions
- [ ] Proxy mode — all API calls routed through ocpgate (v2)
- [ ] Slack/Teams alert on production cluster access

### Migration path

When HashiCorp Boundary is adopted, `ocpgate` retires gracefully:

- **Auth** → replaced by Boundary's access brokering
- **Session** → replaced by Boundary's session management and recording
- **Audit** → Boundary has built-in audit; the OpenSearch integration remains
- **Registry** → *stays useful.* Boundary still needs cluster targets, and the
  GitOps registry can feed its target configuration via automation.

The tool is temporary. The GitOps cluster registry pattern survives it.

---

## License

MIT — see [LICENSE](LICENSE).
