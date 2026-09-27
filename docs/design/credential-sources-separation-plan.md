# Credential Sources vs. Environment Sources — Implementation Sketch

**Status:** SKETCH, 2026-09-27 — incomplete, and unstable while questions are open.

> **Precedence.** This is an implementation sketch companion to
> [`credential-sources-separation.md`](credential-sources-separation.md).
> The design doc wins on all behavioral decisions; nothing here makes a design decision.

---

## 1. Affected Codebase Map

| File / Package | Responsibility in this feature |
| :--- | :--- |
| `internal/config/validate.go` | Add validation for `credential_sources`. Add refusal rule checking `env_sources` against provider claims. |
| `internal/config/load.go` | Anchor relative `credential_sources` paths beside the declaring configuration file. |
| `internal/config/envsources.go` | Update `ResolveEnvSourcesFull` to support both `env_sources` and `credential_sources`. |
| `internal/packload/credentialscope.go` | Update `ScopeInput` and `ScopeCredentials` to take both streams. Guarantee that `env_sources` is never filtered and `credential_sources` is partitioned. |
| `internal/cli/host.go` | Update `composeHostVars` to accept `--with-credentials` or map `-p <provider>` for arbitrary host commands (e.g. `bash`). |
| `internal/cli/run/profilechannel.go` | Thread both channels into `composePackChannel`. |
| `internal/cli/run/userenv.go` | Ensure `writeUserEnvFile` receives pure `env_sources` without censoring. |

---

## 2. Planned Changes by Component

### 2.1 Configuration Schema (`internal/config`)

1. **`credential_sources` Key:**
   - Validated identically to `env_sources`: list of strings (file paths) and JSON objects (inline maps).
   - Anchored at load time by `config.AnchorCredentialSources`.
2. **Provider Key Refusal in `env_sources`:**
   - Blocked on [OQ-2](credential-sources-separation.md#OQ-2).
   - If an entry in `env_sources` defines a variable matching any composed provider's `api_key_env_name`, emit a validation error:
     ```go
     if claimedBy := providerClaims[varName]; len(claimedBy) > 0 {
         problems = append(problems, fmt.Sprintf("%s is a provider credential (claimed by %s) and must be declared under credential_sources", varName, strings.Join(claimedBy, ", ")))
     }
     ```

### 2.2 Credential Gate (`internal/packload/credentialscope.go`)

Blocked on [OQ-1](credential-sources-separation.md#OQ-1).

1. **Extend `ScopeInput`:**
   ```go
   type ScopeInput struct {
       EnvSources        *jsonx.OrderedMap // General environment (unfiltered)
       CredentialSources *jsonx.OrderedMap // Explicit credentials (filtered)
       // ...
   }
   ```
2. **Partitioning:**
   - `sharedEnvSources`: Direct copy of `EnvSources` (no keys withheld).
   - `AgentDelivery.EnvSources`: Populated strictly from `CredentialSources` matching that agent's provider claims.

### 2.3 Host Ad-hoc Command Delivery (`internal/cli/host.go`)

Blocked on [OQ-3](credential-sources-separation.md#OQ-3).

1. In `composeHostVars`:
   - If `agent` is an unknown/ad-hoc binary (e.g. `bash`):
   - Check if the user specified `-p <provider>` or `--with-credentials <provider>`.
   - If specified, treat the command as an ad-hoc recipient for that provider's credentials.
   - Inject the provider's `api_key_env_name` directly into `c.vars`.

---

## 3. Test Strategy

1. **Schema Validation Tests:**
   - `credential_sources` parses dotenv files and inline maps.
   - If provider keys appear in `env_sources`, verify refusal behavior.
2. **Gate Partitioning Tests (`internal/packload`):**
   - General variables in `env_sources` (`PORT`, `DEBUG`) reach all agents and shared environment.
   - Secrets in `credential_sources` (`ZAI_API_KEY`) reach only the agent selecting `zai`.
3. **Host Execution Tests (`internal/cli`):**
   - `yolo host -p zai -- bash -c 'echo $ZAI_API_KEY'` delivers the key.
   - `yolo host -- bash -c 'echo $ZAI_API_KEY'` withholds the key with clear disclosure.
   - General variables (`PORT`) always pass through to `bash`.
