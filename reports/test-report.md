# ocpgate test report

| | |
|---|---|
| Result | **PASS** |
| Target | `fakes` |
| Generated | 2026-08-14T08:13:41Z |
| Commit | 909849c (dirty) |
| Go | go1.24.5 darwin/arm64 |
| Race detector | enabled |
| Total coverage | 67.1% |

Tests: **100 passed**, **0 failed**, 0 skipped
(including subtests: 117 passed, 0 failed)

## Packages

| Package | Result | Time | Coverage |
|---|---|---|---|
| `cmd/ocpgate` | ok | 3.706s | 62.6% |
| `internal/audit` | ok | 2.302s | 90.0% |
| `internal/auth` | ok | 2.585s | 82.1% |
| `internal/ocp` | no tests | — | 0.0% |
| `internal/registry` | ok | 2.956s | 85.9% |
| `internal/retry` | ok | 3.255s | 91.7% |
| `internal/session` | ok | 3.566s | 78.9% |
| `internal/tui/keys` | no tests | — | 0.0% |
| `internal/tui/styles` | no tests | — | — |
| `internal/tui/views` | ok | 2.097s | 50.0% |
| `internal/tui` | ok | 7.978s | 78.4% |
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
github.com/oziie/ocpgate/internal/auth/ocp_oauth.go:56:			WithInsecureSkipTLSVerify	0.0%
github.com/oziie/ocpgate/internal/ocp/namespaces.go:30:			ListNamespaces			0.0%
github.com/oziie/ocpgate/internal/ocp/namespaces.go:65:			classifyAPIError		0.0%
github.com/oziie/ocpgate/internal/registry/registry.go:34:		Error				0.0%
github.com/oziie/ocpgate/internal/retry/retry.go:45:			Error				0.0%
github.com/oziie/ocpgate/internal/retry/retry.go:46:			Unwrap				0.0%
github.com/oziie/ocpgate/internal/session/environ.go:15:		Environ				0.0%
github.com/oziie/ocpgate/internal/session/environ.go:32:		LoginShell			0.0%
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
