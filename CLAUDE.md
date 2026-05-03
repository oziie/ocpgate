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

- **Language:** Go 1.22+
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
│       └── main.go                    # Entrypoint, cobra root command setup
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
│   │   ├── types.go                   # Session struct
│   │   └── session_test.go
│   │
│   ├── audit/
│   │   ├── audit.go                   # AuditLogger interface + event builder
│   │   ├── stdout.go                  # JSON stdout writer (Fluentd picks up)
│   │   ├── types.go                   # AuditEvent, EventType, Outcome structs
│   │   └── audit_test.go
│   │
│   └── tui/
│       ├── app.go                     # Root Bubble Tea model + update loop
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
    Username    string
    KubeconfigPath string
    StartedAt   time.Time
    ExpiresAt   time.Time
}

type SessionManager interface {
    Start(cluster registry.ClusterEntry, auth auth.AuthResult) (*Session, error)
    End(session *Session) error
    IsExpired(session *Session) bool
}
```

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
    Timestamp   time.Time
    EventType   EventType
    Username    string
    ClusterName string
    Environment string
    SessionID   string
    Outcome     Outcome
    Message     string
    SourceHost  string
    SourceIP    string
}

type AuditLogger interface {
    Log(event AuditEvent)
}
```

---

## OCP OAuth Auth Flow

```
User enters LDAP credentials (TUI masked input)
        │
        ▼
POST https://<cluster-api>/oauth/authorize
     ?response_type=token
     &client_id=openshift-challenging-client
  Authorization: Basic base64(user:pass)
        │
        ▼
OCP OAuth validates against LDAP backend
        │
        ▼
302 redirect with token in Location header fragment
        │
        ▼
Parse token + expiry from Location header
        │
        ▼
Return AuthResult { Token, ExpiresAt, Username }
```

Note: OCP's `openshift-challenging-client` is the built-in OAuth client that
accepts Basic auth and returns a Bearer token. This is the standard OCP CLI auth
flow used by `oc login`.

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

1. `pkg/config` — config loader first, everything depends on it
2. `pkg/version` — version injection via ldflags
3. `internal/registry` — GitLab sync + local cache
4. `internal/auth` — OCP OAuth flow
5. `internal/session` — temp kubeconfig + cleanup
6. `internal/audit` — stdout JSON logger
7. `cmd/ocpgate` — cobra CLI, wire everything together
8. Non-interactive CLI mode: `ocpgate connect <cluster>` working end to end
9. `internal/tui` — Bubble Tea views, built on top of working CLI foundation
10. Hardening — signal handling, token expiry, error UX, retry logic

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