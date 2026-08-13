# ocpgate test report

| | |
|---|---|
| Result | **PASS** |
| Target | `fakes` |
| Generated | 2026-08-13T07:20:58Z |
| Commit | 97f4f68 (dirty) |
| Go | go1.24.5 darwin/arm64 |
| Race detector | enabled |
| Total coverage | 58.9% |

Tests: **61 passed**, **0 failed**, 0 skipped
(including subtests: 78 passed, 0 failed)

## Packages

| Package | Result | Time | Coverage |
|---|---|---|---|
| `cmd/ocpgate` | ok | 1.624s | 61.3% |
| `internal/audit` | ok | 1.642s | 90.0% |
| `internal/auth` | ok | 2.204s | 78.8% |
| `internal/ocp` | no tests | — | 0.0% |
| `internal/registry` | ok | 2.050s | 34.2% |
| `internal/session` | ok | 1.218s | 73.2% |
| `internal/tui/keys` | no tests | — | 0.0% |
| `internal/tui/styles` | no tests | — | — |
| `internal/tui/views` | ok | 2.305s | 51.8% |
| `internal/tui` | ok | 7.046s | 72.9% |
| `pkg/config` | no tests | — | 0.0% |
| `pkg/version` | no tests | — | 0.0% |

## Least-covered functions

```
github.com/oziie/ocpgate/cmd/ocpgate/main.go:19:			main				0.0%
github.com/oziie/ocpgate/cmd/ocpgate/sessions.go:54:			plural				0.0%
github.com/oziie/ocpgate/cmd/ocpgate/tui.go:26:				runTUI				0.0%
github.com/oziie/ocpgate/cmd/ocpgate/tui.go:67:				isTerminal			0.0%
github.com/oziie/ocpgate/internal/audit/audit.go:70:			Log				0.0%
github.com/oziie/ocpgate/internal/audit/stdout.go:29:			NewStdoutLogger			0.0%
github.com/oziie/ocpgate/internal/auth/auth.go:32:			Error				0.0%
github.com/oziie/ocpgate/internal/auth/ocp_oauth.go:53:			WithInsecureSkipTLSVerify	0.0%
github.com/oziie/ocpgate/internal/ocp/namespaces.go:28:			ListNamespaces			0.0%
github.com/oziie/ocpgate/internal/registry/gitlab.go:132:		List				0.0%
github.com/oziie/ocpgate/internal/registry/gitlab.go:141:		Get				0.0%
github.com/oziie/ocpgate/internal/registry/gitlab.go:154:		LastSynced			0.0%
github.com/oziie/ocpgate/internal/registry/gitlab.go:160:		isYAML				0.0%
github.com/oziie/ocpgate/internal/registry/gitlab.go:38:		NewGitLabRegistry		0.0%
github.com/oziie/ocpgate/internal/registry/gitlab.go:80:		Sync				0.0%
```

## What this run proves

This run exercised **fakes only**. The OCP OAuth server, the cluster
API, and GitLab were all stubbed in-process, so it confirms ocpgate's
own logic and nothing about a real cluster's behavior.

Assumptions still unverified against real infrastructure:

- `/.well-known/oauth-authorization-server` returns `authorization_endpoint`
- `X-CSRF-Token: 1` is enough to get a Basic challenge, not the HTML login page
- the token arrives in the redirect's URL **fragment** as `access_token` + `expires_in`
- 401/403 from the authorize endpoint means bad credentials, not a disabled
  account or an LDAP timeout
- namespace listing is normally forbidden for ordinary users (the text-field
  fallback), and `oc get projects` is not needed instead

Re-run with `OCPGATE_TEST_TARGET=<cluster>` once pointed at real infrastructure.

---
_Full output: `reports/test-log.txt`. Coverage profile: `reports/coverage.out`_
_Regenerate with `make test-report`._
