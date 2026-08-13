# ocpgate — Claude Code Project Context

## What Is This Project

`ocpgate` is a TUI-first CLI tool written in Go that solves a specific DevOps problem:
engineers need access to multiple OCP (OpenShift Container Platform) clusters but
kubeconfigs are scattered across per-installer machines with no central entry point,
no audit trail, and no unified auth flow.

This is an **interim solution** designed to bridge current state toward a full
HashiCorp Vault + Boundary adoption. It is also a portfolio project demonstrating
senior-level infrastructure tooling design.

---

## Problem Statement

- Multiple OCP clusters exist across environments (prod, staging, dev)
- Kubeconfigs are stored per-installer machine — no centralized truth source
- Engineers SSH to installer machines to run cluster commands — slow, insecure, unaudited
- No audit trail of who accessed which cluster, when, and from where
- LDAP is the existing auth backbone — OCP clusters sync users via a Job against LDAP groups

---

## What ocpgate Does

1. Engineer launches `ocpgate` — TUI opens with a searchable cluster list
2. Engineer selects a cluster by name
3. Tool prompts for LDAP credentials (masked input, never stored)
4. Tool authenticates against the selected cluster's OCP OAuth endpoint (LDAP-backed)
5. OCP returns a short-lived Bearer token
6. Tool writes a temporary kubeconfig to `~/.cache/ocpgate/sessions/<session-id>/`
7. Tool sets `KUBECONFIG` env and drops engineer into an active session
8. All events (auth attempt, session start/end, token expiry) are logged as structured
   JSON to stdout — picked up by existing Fluentd collector → OpenSearch
9. On exit, temporary kubeconfig is deleted (defer + signal handler)

---

## Architecture

```
ocpgate (TUI)
├── Registry Module     → GitLab API sync → local cache
├── Auth Module         → OCP OAuth + LDAP backend → short-lived token
├── Session Module      → temp kubeconfig writer + cleanup
├── Audit Module        → structured JSON → stdout → Fluentd → OpenSearch
└── TUI Layer           → Bubble Tea (cluster list, ns selector, status bar)

External Dependencies:
├── GitLab Repo         → cluster-registry (GitOps-managed cluster metadata)
├── OCP OAuth Endpoint  → per-cluster, LDAP-backed
├── Fluentd             → already running, collects stdout from apps
└── OpenSearch          → existing cluster, receives ocpgate-audit-* index
```

---

## Key Design Decisions (see docs/decisions/ for full ADRs)

| Decision | Choice | Reason |
|---|---|---|
| Language | Go | Single binary, k9s ecosystem, Bubble Tea TUI framework |
| Auth approach | Stateless — token per session, never stored | Security, simplicity, LDAP already handles identity |
| Cluster registry | GitLab repo (GitOps) | No central infra needed, MR-based governance, audit trail |
| Kubeconfig storage | None — generated per session, deleted on exit | No static secrets, short-lived tokens only |
| Audit destination | stdout → Fluentd → OpenSearch | Reuses existing log pipeline, zero new infra |
| Mode | Passthrough (not proxy) | 80% audit value at 20% proxy complexity — proxy is v2 |
| TUI framework | Bubble Tea + Bubbles + Lip Gloss | Same ecosystem as k9s, mature, single binary output |

---

## Tech Stack

- **Language:** Go 1.24+ (floor set by `k8s.io/client-go`, pinned to v0.31.x —
  later client-go releases push the `go` directive to 1.26)
- **TUI:** github.com/charmbracelet/bubbletea
- **TUI Components:** github.com/charmbracelet/bubbles
- **TUI Styling:** github.com/charmbracelet/lipgloss
- **CLI framework:** github.com/spf13/cobra
- **Config:** github.com/spf13/viper
- **GitLab client:** github.com/xanzy/go-gitlab
- **Kubernetes client:** k8s.io/client-go
- **Logging:** github.com/rs/zerolog (structured JSON)
- **UUID:** github.com/google/uuid
- **Testing:** standard library + github.com/stretchr/testify

---

## Project Structure

```
ocpgate/
├── CLAUDE.md                          # This file — Claude Code context
├── README.md
├── go.mod
├── go.sum
├── Makefile
├── .goreleaser.yml
│
├── cmd/
│   └── ocpgate/
│       ├── main.go                    # Entrypoint + top-level signal context
│       ├── root.go                    # Cobra root, shared app deps, registry bootstrap
│       ├── connect.go                 # `connect` — auth → session → subshell → cleanup
│       ├── clusters.go                # `clusters list` / `clusters sync`
│       ├── sessions.go                # `sessions prune` — clean up after crashed runs
│       ├── prompt.go                  # Username + masked password prompts
│       └── connect_test.go            # End-to-end CLI test against a fake OCP server
│
├── internal/
│   ├── registry/
│   │   ├── registry.go                # ClusterRegistry interface
│   │   ├── gitlab.go                  # GitLab API sync logic
│   │   ├── cache.go                   # Local disk cache (JSON)
│   │   ├── types.go                   # ClusterEntry struct
│   │   └── registry_test.go
│   │
│   ├── auth/
│   │   ├── auth.go                    # Authenticator interface
│   │   ├── ocp_oauth.go               # OCP OAuth token request flow
│   │   ├── types.go                   # AuthResult, Credentials structs
│   │   └── auth_test.go
│   │
│   ├── session/
│   │   ├── session.go                 # Session lifecycle manager
│   │   ├── kubeconfig.go              # Temp kubeconfig writer + cleanup
│   │   ├── environ.go                 # KUBECONFIG env for subshells
│   │   ├── expiry.go                  # Token expiry watcher + countdown format
│   │   ├── types.go                   # Session struct
│   │   └── session_test.go
│   │
│   ├── audit/
│   │   ├── audit.go                   # AuditLogger interface + event builder
│   │   ├── stdout.go                  # JSON stdout writer (Fluentd picks up)
│   │   ├── types.go                   # AuditEvent, EventType, Outcome structs
│   │   └── audit_test.go
│   │
│   ├── ocp/
│   │   └── namespaces.go              # Namespace lookup for the TUI selector
│   │
│   ├── retry/
│   │   ├── retry.go                   # Backoff + transient/permanent classification
│   │   └── retry_test.go
│   │
│   └── tui/
│       ├── app.go                     # Root Bubble Tea model + update loop
│       ├── run.go                     # Program startup + guaranteed session cleanup
│       ├── views/
│       │   ├── cluster_list.go        # Cluster selection view
│       │   ├── credentials.go         # LDAP credential input view (masked)
│       │   ├── namespace.go           # Namespace selector view
│       │   └── status_bar.go          # Persistent bottom bar (user/cluster/token expiry)
│       ├── keys/
│       │   └── keymap.go              # All keybindings defined here
│       └── styles/
│           └── styles.go              # Lip Gloss style definitions
│
├── pkg/
│   ├── config/
│   │   ├── config.go                  # Config loader (viper)
│   │   └── types.go                   # Config struct
│   └── version/
│       └── version.go                 # Build version info (injected via ldflags)
│
├── docs/
│   └── decisions/
│       ├── 001-use-gitlab-as-cluster-registry.md
│       ├── 002-stateless-token-approach.md
│       ├── 003-stdout-audit-logging-for-fluentd.md
│       └── 004-interim-solution-boundary-migration-path.md
│
├── cluster-registry/                  # Separate GitLab repo — kept here for reference
│   ├── clusters/
│   │   ├── example-prod-cluster-1.yaml
│   │   └── example-staging-cluster-1.yaml
│   ├── schema/
│   │   └── cluster.schema.json
│   ├── scripts/
│   │   └── validate.py
│   └── .gitlab-ci.yml
│
└── deployments/
    └── opensearch/
        ├── index-template.json        # ocpgate-audit-* index template
        └── dashboard.ndjson           # OpenSearch Dashboards export
```

---

## Module Contracts (Interfaces)

### Registry
```go
type ClusterEntry struct {
    Name        string `yaml:"name"`
    APIEndpoint string `yaml:"api_endpoint"`
    Environment string `yaml:"environment"` // production | test
    Region      string `yaml:"region"`
    LDAPRealm   string `yaml:"ldap_realm"`
    Active      bool   `yaml:"active"`
}

type Registry interface {
    Sync(ctx context.Context) error
    List() ([]ClusterEntry, error)
    Get(name string) (*ClusterEntry, error)
    LastSynced() time.Time
}
```

### Auth
```go
type Credentials struct {
    Username string
    Password string // never logged, never stored
}

type AuthResult struct {
    Token     string
    ExpiresAt time.Time
    Username  string
}

type Authenticator interface {
    Authenticate(ctx context.Context, cluster registry.ClusterEntry, creds Credentials) (*AuthResult, error)
}
```

### Session
```go
type Session struct {
    ID          string
    ClusterName string
    Environment string   // carried so audit events need no registry lookup
    Username    string
    Namespace   string
    Dir         string   // per-session dir; removed wholesale by End
    KubeconfigPath string
    StartedAt   time.Time
    ExpiresAt   time.Time
}

// Implemented by session.FileManager.
type Manager interface {
    Start(cluster registry.ClusterEntry, result auth.AuthResult) (*Session, error)
    End(session *Session) error   // idempotent: defer and signal handler may both fire
    IsExpired(session *Session) bool
}
```

`FileManager` also exposes `PruneStale(maxAge)`, which removes session
directories left behind by processes killed before their cleanup ran
(SIGKILL, crash, power loss). Exposed as `ocpgate sessions prune`.

`End` refuses to remove any path outside its base directory — it takes a
caller-supplied path and hands it to `RemoveAll`, so that guard is what
keeps a malformed `Session` from becoming a recursive delete.

### Audit
```go
type EventType string
const (
    EventAuthAttempt  EventType = "auth_attempt"
    EventAuthFailure  EventType = "auth_failure"
    EventSessionStart EventType = "session_start"
    EventSessionEnd   EventType = "session_end"
    EventRegistrySync EventType = "registry_sync"
    EventTokenExpired EventType = "token_expired"
)

type Outcome string
const (
    OutcomeSuccess Outcome = "success"
    OutcomeFailure Outcome = "failure"
)

type AuditEvent struct {
    Timestamp   time.Time   // filled in by the logger when zero
    EventType   EventType
    Username    string
    ClusterName string
    Environment string
    APIEndpoint string      // -> cluster.api_endpoint in the emitted JSON
    SessionID   string
    TokenExpiry time.Time   // -> session.token_expiry
    Outcome     Outcome
    Message     string
    SourceHost  string      // filled in by the logger when empty
    SourceIP    string      // filled in by the logger when empty
}

// Named audit.Logger, not audit.AuditLogger, to avoid the stutter.
// Implemented by audit.StdoutLogger and audit.NopLogger (audit.enabled: false).
type Logger interface {
    Log(event AuditEvent)
}
```

Empty `user` / `cluster` / `session` / `source` blocks are omitted from the
emitted JSON rather than written as empty objects. Encoding goes through
zerolog because it writes each event as a single atomic `Write`, so
concurrent goroutines cannot interleave half-written objects into the
stream Fluentd is tailing.

---

## OCP OAuth Auth Flow

```
User enters LDAP credentials (masked input)
        │
        ▼
GET https://<cluster-api>/.well-known/oauth-authorization-server
        │                      (discovery — unauthenticated)
        ▼
authorization_endpoint  (on a real cluster this is a different host:
                         oauth-openshift.apps.<domain>, not the API host)
        │
        ▼
GET <authorization_endpoint>
     ?response_type=token
     &client_id=openshift-challenging-client
  Authorization: Basic base64(user:pass)
  X-CSRF-Token: 1
        │
        ▼
OCP OAuth validates against LDAP backend
        │
        ▼
302 redirect with token in the Location header fragment
        │
        ▼
Parse token + expires_in from the fragment
        │
        ▼
Return AuthResult { Token, ExpiresAt, Username }
```

Notes:
- `openshift-challenging-client` is the built-in OAuth client that answers
  a Basic-auth challenge with a Bearer token. Same flow as `oc login`.
- `X-CSRF-Token: 1` is required. Without it OCP treats the call as a
  browser session and serves the HTML login page instead of challenging.
- The HTTP client must **not** follow the redirect: the token is in the
  `Location` fragment, and the redirect target must never be fetched.
- Discovery falls back to `<cluster-api>/oauth/authorize` when the
  well-known endpoint is unavailable, rather than failing outright.
- Credentials and tokens have redacting `String()`/`GoString()` methods, so
  an accidental `%v` in a log line or error cannot leak them.

---

## Cluster Registry YAML Schema

```yaml
# cluster-registry/clusters/prod-cluster-1.yaml
name: prod-cluster-1
api_endpoint: https://api.prod-cluster-1.example.com:6443
environment: production          # production | test
region: eu-west
ldap_realm: PROD
active: true
```

Rules:
- `name` must be unique across all files
- `api_endpoint` must be a valid HTTPS URL with port
- `environment` must be one of: production, test
- `active: false` means the cluster is visible but auth is disabled

---

## Audit Log Format (stdout → Fluentd → OpenSearch)

One JSON object per line, written to stdout:

```json
{
  "@timestamp": "2026-05-02T14:32:01Z",
  "tool": "ocpgate",
  "version": "1.0.0",
  "event_type": "session_start",
  "user": {
    "username": "john.doe"
  },
  "cluster": {
    "name": "prod-cluster-1",
    "environment": "production",
    "api_endpoint": "https://api.prod-cluster-1.example.com:6443"
  },
  "session": {
    "id": "550e8400-e29b-41d4-a716-446655440000",
    "token_expiry": "2026-05-02T15:32:01Z"
  },
  "source": {
    "hostname": "johns-macbook",
    "ip": "10.10.1.45"
  },
  "outcome": "success"
}
```

---

## Config File (~/.config/ocpgate/config.yaml)

```yaml
registry:
  gitlab_url: https://gitlab.example.com
  project_id: "123"
  branch: main
  token_env: OCPGATE_GITLAB_TOKEN   # GitLab token read from env var, never stored in config
  cache_path: ~/.cache/ocpgate/clusters.json
  sync_on_start: true

audit:
  enabled: true
  writer: stdout                    # stdout only for now (v1)

tui:
  default_namespace: default
  show_environment_badge: true
```

---

## TUI Layout

```
┌─────────────────────────────────────────────────────────────────┐
│  ocpgate v1.0.0    [↑↓] navigate  [/] search  [enter] connect  [q] quit  │
├─────────────────────────────────────────────────────────────────┤
│  > search clusters...                                           │
├─────────────────────────────────────────────────────────────────┤
│  ● prod-cluster-1      PRODUCTION   eu-west                     │
│  ● prod-cluster-2      PRODUCTION   eu-central                  │
│  ○ test-cluster-1      TEST         eu-west                     │
│  ○ dev-cluster-2       TEST         eu-west                     │
├─────────────────────────────────────────────────────────────────┤
│  user: —   cluster: —   namespace: —   token: —                 │
└─────────────────────────────────────────────────────────────────┘
```

Views (in order):
1. **Cluster List** — searchable, filterable by environment
2. **Credential Input** — username + masked password prompt
3. **Namespace Selector** — list of namespaces in selected cluster
4. **Active Session** — current context shown in status bar, token expiry countdown

---

## Build Order (follow this sequence)

1. ✅ `pkg/config` — config loader first, everything depends on it
2. ✅ `pkg/version` — version injection via ldflags
3. ✅ `internal/registry` — GitLab sync + local cache
4. ✅ `internal/auth` — OCP OAuth flow
5. ✅ `internal/session` — temp kubeconfig + cleanup
6. ✅ `internal/audit` — stdout JSON logger
7. ✅ `cmd/ocpgate` — cobra CLI, wire everything together
8. ✅ Non-interactive CLI mode: `ocpgate connect <cluster>` working end to end
9. ✅ `internal/tui` — Bubble Tea views, built on top of working CLI foundation
10. ⬜ Hardening — signal handling, token expiry, error UX, retry logic

Steps 1–9 are done and covered by tests, including an end-to-end CLI test
that drives `connect` against a fake OCP OAuth server, and a TUI test that
drives the full model flow with fake collaborators.

Step 10 is largely done: signal handling around the subshell,
stale-session pruning, the non-terminal fallback, retry logic
(`internal/retry`), and token-expiry warnings (`session.ExpiryWatcher`).
What remains is the error-UX pass, deliberately deferred until a real
cluster exists — the failure messages worth writing are the ones a real
OAuth server actually produces, not the ones the fakes were built to
produce.

## Remaining Work (picked up 2026-08-13)

Ordered roughly by what unblocks what.

### 1. Blocked on a real cluster

Ozan is standing up OCP cluster(s) and will supply the real cluster names,
environments, regions, LDAP realms, and usernames. Until then these stay open:

- [ ] **Verify the five OAuth assumptions** the fakes encode (listed under
      [Testing](#testing)). Each one is a guess about real OCP behavior.
- [ ] **Error-UX pass** — the deferred half of step 10. Write messages for
      the failure modes fakes cannot produce: a disabled LDAP account vs a
      wrong password (both arrive as 403), the API reachable but the OAuth
      route not, a custom CA rejected by TLS verification, a token that
      expires mid-command.
- [ ] **Namespace listing** — confirm whether ordinary users must use the
      OCP project API (`project.openshift.io`) instead of `namespaces`. If
      so, `internal/ocp` needs a second code path.
- [ ] Run `OCPGATE_TEST_TARGET=<cluster> make test-report` and diff against
      the committed fakes baseline.

### 2. Coverage gaps (no cluster needed)

- [ ] `internal/ocp` — 0.0%, no test file. `classifyAPIError` in particular
      encodes real assumptions and is entirely unexercised.
- [ ] `pkg/config` — 0.0%, no test file. Loading, env-var override,
      `expandHome`, and `GitLabToken` are all untested.
- [ ] `internal/tui/views` — 50.0%. The list delegates and `Update` paths
      are the untested parts.
- [ ] `cmd/ocpgate` — 62.6%.

### 3. Scaffolding from the project structure that was never built

These directories exist in the tree above but are empty:

- [ ] `docs/decisions/` — the four ADRs referenced throughout this file
- [ ] `cluster-registry/` — example cluster YAMLs, `cluster.schema.json`,
      `validate.py`, `.gitlab-ci.yml`. Worth building alongside the real
      registry repo so the schema matches what Ozan actually defines.
- [ ] `deployments/opensearch/` — `index-template.json` (must exist before
      the first audit log lands) and `dashboard.ndjson`
- [ ] `.goreleaser.yml` — single-binary release pipeline

### 4. Deliberately out of scope for v1

Proxy mode, web UI, Slack/Teams alerts on production access, Vault
integration, Boundary migration. See [Future / Out of Scope](#future--out-of-scope-for-v1).

## Retry policy (`internal/retry`)

One retry layer for every network call, with a single distinction that
drives everything: **transient versus definitive**. Callers mark definitive
failures with `retry.Permanent(err)`; anything else is retried with
exponential backoff and equal jitter. The wrapper is stripped before the
error reaches the caller, so sentinel matching still works.

What is *not* retried, and why it matters:

- **Rejected credentials** (401/403 from the OAuth server). Retrying a bad
  password cannot succeed and is a good way to trip an LDAP account lockout.
- **A rejected GitLab token** or missing project (any 4xx except 429).
- **A malformed OAuth redirect** — a protocol mismatch, not a blip.
- **A 404 on OAuth discovery** — that cluster simply does not publish it,
  so retrying would slow every login on such a cluster.

Retried: network failures, 429, and 5xx.

**Do not layer this on a client that already retries.** go-gitlab wraps its
transport in `retryablehttp` with `RetryMax: 5` and a backoff running to
30s; nested inside our loop that is up to 15 attempts and minutes of
sleeping behind a prompt, and it has no notion of permanence. The GitLab
client is therefore constructed with `gitlab.WithoutRetries()`.

## Token expiry (`session.ExpiryWatcher`)

The token expires silently — kubectl just starts returning 401, which
reads like a broken cluster rather than a finished session. So expiry is
surfaced twice over:

- `connect` runs a watcher goroutine that warns on stderr at 15m, 5m, and
  1m remaining, then once at expiry. A shell has no status bar, so the
  warning has to come to the engineer.
- The TUI's existing one-second tick notices the lapse and shows it.

Thresholds already in the past when a session starts are skipped, so a
short-lived token does not open with a burst of warnings.

Both paths emit `token_expired`, and both guard against emitting it twice
(`app.expiryLogged` / `Model.expiryLogged`) — the watcher usually gets
there first, and teardown covers a process suspended straight through the
expiry.

## Testing

Everything so far is tested against **fakes**: the OCP OAuth server, the
cluster API, and GitLab are all stubbed in-process. The fakes live in
`internal/auth/auth_test.go` (`fakeOCP`), `cmd/ocpgate/connect_test.go`
(`newFakeCluster`), and `internal/tui/app_test.go` (fake authenticator,
session manager, and audit recorder).

`make test-report` (→ `scripts/test-report.sh`) runs the suite with the
race detector and coverage and writes `reports/test-report.md`, archiving a
timestamped copy in `reports/history/`. Reports are stamped with a target
(`OCPGATE_TEST_TARGET`, default `fakes`) so a fakes run and a real-cluster
run stay distinguishable. Only `reports/test-report.md` is committed, as
the current baseline; `reports/history/`, the raw log, and the coverage
profile are gitignored.

When editing that script: `go test` reports packages in three different
line shapes (`ok`, `?`, and a bare indented line for a no-test package that
was still instrumented). All three must be parsed or packages silently
vanish from the report.

Assumptions the fakes encode, still unverified against a real cluster:

- `/.well-known/oauth-authorization-server` returns `authorization_endpoint`
- `X-CSRF-Token: 1` yields a Basic challenge rather than the HTML login page
- the token arrives in the redirect's URL fragment (`access_token`, `expires_in`)
- 401/403 means bad credentials, not a disabled account or LDAP timeout
- namespace listing is normally forbidden for ordinary users, and the OCP
  project API is not needed instead

## TUI (step 9, as built)

State machine in `internal/tui/app.go`:

```
stateClusters → stateCredentials → stateNamespaces → stateSession
     ↑                  │                  │
     └──────── esc ─────┴──────────────────┘   (esc discards any issued token)
```

Asynchronous steps (auth, namespace lookup, session start) run as Bubble
Tea commands and report back as messages, so the update loop never blocks
on the network.

Notable decisions:

- **The TUI renders to stderr**, via `tea.WithOutput(os.Stderr)`. stdout
  stays reserved for the audit JSON stream, so `ocpgate 1>>audit.log`
  works while the interface is on screen. This is why `runTUI` requires
  *both* stdin and stderr to be terminals before launching — checking only
  stdin would let `ocpgate 2>log` past the guard and then fail inside
  Bubble Tea. Without a terminal it falls back to `--help`.
- **Session cleanup lives in `tui.Run`, not in the model.** Bubble Tea has
  no teardown hook, and the kubeconfig must be removed even when the
  program exits through Ctrl-C.
- **The namespace selector has two shapes.** Ordinary OCP users usually
  cannot list namespaces cluster-wide, so `ErrNamespacesForbidden` falls
  back to a free-text field presented as normal, not as an error.
- **`enter` in the session view opens a shell** via `tea.ExecProcess`,
  which suspends the TUI and restores it when the shell exits.
- Views must tolerate being resized before they are constructed: Bubble
  Tea reports the window size at startup, long before the namespace view
  exists.

## CLI Surface (as built)

```
ocpgate                                  # launch the TUI (help when not on a terminal)
ocpgate clusters list [-e production]    # cluster table, served from the local cache
ocpgate clusters sync                    # force a GitLab sync
ocpgate connect <cluster> [-u user] [-n namespace]
ocpgate connect <cluster> -- oc get pods # run one command instead of a subshell
ocpgate sessions prune [--older-than 24h]
ocpgate version
```

Global flags: `--config`, `--insecure-skip-tls-verify` (mirrors
`oc login --insecure-skip-tls-verify`; when set, the generated kubeconfig
records it too so kubectl behaves the way ocpgate just did).

### Output convention

- **stdout** — audit JSON, one object per line, for Fluentd. Also the
  cluster table from `clusters list`, since that is what a user piping the
  command wants.
- **stderr** — all human-facing status, prompts, and warnings.

This keeps `ocpgate connect ... 1>>audit.log` a clean audit stream while
the engineer still sees prompts and the session banner.

### Degraded-mode behavior

`clusters list` and `connect` work from the local cache when GitLab is
unreachable or `OCPGATE_GITLAB_TOKEN` is unset — sync failure warns on
stderr rather than blocking access to a cluster the engineer already knows
about. An explicit `clusters sync` still fails loudly.

---

## OpenSearch Integration

Index pattern: `ocpgate-audit-YYYY.MM.DD` (daily rollover)

Apply index template from `deployments/opensearch/index-template.json` before
first log lands. Fluentd routes logs with `"tool":"ocpgate"` field to this index.

Key fields mapped as `keyword` for filtering:
- `event_type`, `outcome`, `user.username`, `cluster.name`, `cluster.environment`, `session.id`

---

## Future / Out of Scope for v1

- Proxy mode (all API calls routed through ocpgate — v2)
- Web UI companion for security team dashboard
- Slack/Teams alert on prod cluster access
- HashiCorp Boundary migration (planned long-term replacement)
- HashiCorp Vault integration for token storage (when Vault is adopted)
- Open source release with pluggable auth backends

---

## Migration Path to Boundary

When HashiCorp Boundary is adopted:
- Auth module → replaced by Boundary's access brokering
- Session module → replaced by Boundary's session management + recording
- Audit module → Boundary has built-in audit, OpenSearch integration remains
- **Registry module → stays useful** — Boundary still needs cluster targets,
  the GitLab GitOps registry can feed Boundary's target configuration via automation

ocpgate retires gracefully. The GitOps cluster registry pattern survives.