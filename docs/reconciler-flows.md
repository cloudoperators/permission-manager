# Reconciler Flows

Visual diagrams of the pm-controller's reconciliation logic. Each provider is an independent binary with its own reconciler loop.

---

## 1. CRD Relationships & Data Flow

How the Custom Resources relate to each other and what each provider produces.

### Input: What the user defines

```
┌─────────────────────────────────────────────────────────────────┐
│  ApplicationSpecificRole (ASR)                                  │
│                                                                 │
│  spec:                                                          │
│    connectorRefs: [cb-kubernetes, cb-greenhouse-teams]           │
│    permissions:                                                  │
│      - resource: urn:ccrn:cluster.k8s.../v1/nw-a-*              │
│        capability: READ                                         │
│      - resource: urn:ccrn:namespace.k8s.../v1/*/monitoring      │
│        capability: WRITE                                        │
└──────────────────────────┬──────────────────────────────────────┘
                           │ references
                           ▼
┌─────────────────────────────────────────────────────────────────┐
│  ConnectorBinding                                               │
│                                                                 │
│  spec:                                                          │
│    provider: kubernetes | github | greenhouse-teams              │
│    definition:                                                   │
│      capabilityMapping: {READ: cluster-viewer, WRITE: admin}    │
│      defaultTargetNamespace: greenhouse-org-foo                  │
└──────────────────────────┬──────────────────────────────────────┘
                           │ credentials from
                           ▼
┌─────────────────────────────────────────────────────────────────┐
│  Secret (per cluster)                                           │
│                                                                 │
│  data:                                                          │
│    greenhousekubeconfig: <base64 kubeconfig>                    │
└─────────────────────────────────────────────────────────────────┘
```

### Processing: Provider routing

```mermaid
flowchart LR
    ASR([ASR]) --> CB{{"ConnectorBinding<br/>routes to provider"}}

    CB -->|"provider: kubernetes"| K8S["☸ Kubernetes Provider"]
    CB -->|"provider: greenhouse-teams"| GH["🌿 GreenhouseTeams Provider"]
    CB -->|"provider: github"| GIT["🐙 GitHub Provider"]
```

### Output: What each provider creates

| Provider | Where | Resources Created |
|----------|-------|-------------------|
| **kubernetes** | Remote target clusters | `ClusterRole pm-‹asr›` (cluster verbs) <br/> `ClusterRole pm-‹asr›-ns` (namespace verbs) <br/> `ClusterRoleBinding pm-‹asr›` <br/> `RoleBinding pm-‹asr›` (per namespace) |
| **greenhouse-teams** | Greenhouse org namespace (management cluster) | `Team pm-‹teamName›` <br/> `TeamRoleBinding pm-‹asr›-‹role›-‹cluster›` |
| **github** | Provider namespace (management cluster) | `GithubTeam` <br/> `GithubTeamRepository` |
| **status-aggregator** | ASR status (management cluster) | `status.conditions[Ready, Degraded]` |

All output resources carry ownership labels for discovery and cleanup:
```yaml
labels:
  managed-by: pm-controller          # or pm-greenhouse-teams
  permission-manager.cloudoperators.dev/asr: <asr-name>
```

---

## 2. Kubernetes Provider — Create/Update Reconcile

The reconcile loop is split into three stages for clarity.

### Stage 1: Filtering & Setup

```mermaid
flowchart TD
    START(["⚡ Reconcile triggered<br/>(ASR create/update/requeue)"])

    START --> FETCH["Fetch ASR by NamespacedName"]
    FETCH -->|"Not found"| GONE["Return — ASR already deleted"]

    FETCH -->|"Found"| RELEVANT{"Does ASR reference a<br/>kubernetes ConnectorBinding?<br/><i>(O(1) check via CB index)</i>"}

    RELEVANT -->|"No"| STALE{"Still has our finalizer?"}
    STALE -->|"No"| SKIP["Return OK — not our concern"]
    STALE -->|"Yes"| CLEANUP_STALE["Clean up leftover RBAC<br/>Remove finalizer"]
    CLEANUP_STALE --> SKIP

    RELEVANT -->|"Yes"| DELETED{"DeletionTimestamp set?"}
    DELETED -->|"Yes"| DEL_LINK(["→ See Deletion Flow below"])

    DELETED -->|"No"| FINALIZER["Ensure finalizer present<br/><i>permission-manager.cloudoperators.dev/kubernetes</i>"]
    FINALIZER --> STAGE2(["→ Stage 2: Build Plan"])

    style GONE fill:#f5f5f5,stroke:#999
    style SKIP fill:#f5f5f5,stroke:#999
    style DEL_LINK fill:#fff3e0,stroke:#e65100
    style STAGE2 fill:#e8f5e9,stroke:#2e7d32
```

### Stage 2: Plan Construction

```mermaid
flowchart TD
    STAGE2(["📐 Build Plan"])

    STAGE2 --> PARSE["Parse CCRNs from spec.permissions[]<br/><i>urn:ccrn:cluster.k8s.../v1/nw-a-*</i><br/><i>urn:ccrn:namespace.k8s.../v1/*/monitoring</i>"]

    PARSE --> RESOLVE["Resolve cluster wildcards<br/>via ClusterInventory.MatchingNames<br/><i>nw-a-* → [nw-a-prod, nw-a-staging, ...]</i>"]

    RESOLVE --> MAP_CAPS["Map capabilities → verb sets<br/><i>READ → [get, list, watch]</i><br/><i>WRITE → [get, list, watch, create, update, patch, delete]</i>"]

    MAP_CAPS --> SPLIT["Split verbs by scope<br/><b>Cluster verbs</b>: from cluster-CCRN permissions<br/><b>Namespace verbs</b>: from namespace-CCRN permissions"]

    SPLIT --> DIFF["Diff against previous targets<br/>in status.providers.kubernetes.targets"]

    DIFF --> PLAN_OUT["Reconcile Plan:<br/>• <b>clustersToProvision</b>: [{cluster, clusterVerbs, nsVerbs, namespaces}]<br/>• <b>orphanClusters</b>: [clusters no longer targeted]"]

    PLAN_OUT --> STAGE3(["→ Stage 3: Execute"])

    style STAGE2 fill:#e8f5e9,stroke:#2e7d32
    style STAGE3 fill:#e8f5e9,stroke:#2e7d32
```

### Stage 3: Execute Plan

```mermaid
flowchart TD
    STAGE3(["🚀 Execute Plan"])

    STAGE3 --> LOOP["For each cluster in plan"]

    LOOP --> CLIENT["Get cached client for cluster<br/><i>(from kubeconfig Secret, TLS cached)</i>"]
    CLIENT -->|"❌ Unreachable"| UNREACH["Record status: Unreachable<br/>Emit warning event"]
    UNREACH --> NEXT

    CLIENT -->|"✅ Connected"| EXPAND["Expand namespace wildcards<br/><i>List namespaces on remote cluster</i><br/><i>(60s TTL cache)</i>"]

    EXPAND --> BUILD_RBAC["Build RBAC resources<br/>(scope-separated, per ADR-015)"]

    subgraph rbac ["Scope-Separated ClusterRoles"]
        direction LR
        CRC["<b>pm-‹asr›</b><br/>ClusterRole with cluster verbs<br/>→ bound by ClusterRoleBinding"]
        CRN["<b>pm-‹asr›-ns</b><br/>ClusterRole with ns verbs<br/>→ bound by RoleBinding per NS"]
    end

    BUILD_RBAC --> rbac
    rbac --> APPLY["Apply via CreateOrPatch<br/>Delete stale scope resources"]
    APPLY -->|"Error"| ERR["Record status: Error"]
    APPLY -->|"OK"| OK["Record status: Success"]

    ERR --> NEXT
    OK --> NEXT["Next cluster"]

    NEXT --> ORPHANS{"Orphan clusters to clean?"}
    ORPHANS -->|"Yes"| CLEAN_ORPHAN["Delete RBAC on clusters<br/>no longer targeted by this ASR"]
    ORPHANS -->|"No"| PATCH

    CLEAN_ORPHAN --> PATCH["Patch provider status via SSA<br/><i>FieldOwner: pm-controller-kubernetes</i><br/><i>Skip if unchanged</i>"]

    PATCH --> RESULT{"Any errors occurred?"}
    RESULT -->|"Yes"| REQUEUE["Requeue with backoff"]
    RESULT -->|"No"| DONE["✅ Done"]

    style rbac fill:#f1f8e9,stroke:#558b2f
    style DONE fill:#e8f5e9,stroke:#2e7d32
```

---

## 3. Kubernetes Provider — Deletion Flow

When an ASR is deleted, the finalizer blocks removal until all RBAC is confirmed cleaned.

```mermaid
flowchart TD
    DEL(["🗑️ ASR Deletion<br/>(deletionTimestamp set)"])

    DEL --> HAS_FIN{"Has kubernetes finalizer?"}
    HAS_FIN -->|"No"| PROCEED["ASR deletion proceeds<br/>(not our responsibility)"]

    HAS_FIN -->|"Yes"| TARGETS["Determine target clusters<br/><i>From status.providers.kubernetes.targets</i><br/><i>Fallback: full ClusterInventory</i>"]

    TARGETS --> CLEANUP["CleanupAll:<br/>For each cluster → get client → delete by label<br/>(ClusterRoles, ClusterRoleBindings, RoleBindings)"]

    CLEANUP --> RESULT{"Result?"}

    %% Happy path
    RESULT -->|"✅ All clusters cleaned"| REMOVE_FIN["Remove finalizer"]
    REMOVE_FIN --> DELETED["✅ ASR deletion completes"]

    %% Some unreachable
    RESULT -->|"⚠️ Some clusters unreachable"| FILTER["Filter out clusters<br/>gone from inventory<br/><i>(decommissioned = no cleanup needed)</i>"]

    FILTER --> REMAINING{"Orphaned clusters remain?"}
    REMAINING -->|"No"| REMOVE_FIN

    REMAINING -->|"Yes"| ABANDON{"Abandon annotation set?<br/><i>abandon-cleanup: 'true'</i>"}

    %% Exit path 3: abandon
    ABANDON -->|"Yes"| EMIT_ABANDON["Emit CleanupAbandoned event<br/>(operator accepted risk)"]
    EMIT_ABANDON --> REMOVE_FIN

    %% Hold finalizer
    ABANDON -->|"No"| HOLD["🔒 HOLD finalizer<br/>Block ASR deletion"]
    HOLD --> STATUS_ORPHAN["Update status with orphaned clusters"]
    STATUS_ORPHAN --> EMIT_WARN["Emit CleanupBlocked warning"]
    EMIT_WARN --> REQUEUE["Requeue after delay<br/><i>30s initially, escalates to 5m</i>"]

    %% Styling
    style DELETED fill:#e8f5e9,stroke:#2e7d32
    style HOLD fill:#ffebee,stroke:#c62828
    style PROCEED fill:#f5f5f5,stroke:#999
```

**Three exit paths (per ADR-012):**

| # | Condition | Outcome |
|---|-----------|---------|
| 1 | All clusters cleaned successfully | Finalizer removed → deletion proceeds |
| 2 | Cluster gone from Greenhouse inventory | Treated as cleaned (no cleanup needed) → finalizer removed |
| 3 | Operator sets `abandon-cleanup: "true"` | Explicit risk acceptance → finalizer removed despite orphans |

If none of these exit paths are met, the **finalizer is held** — the ASR cannot be deleted while RBAC may still be active on unreachable clusters. The controller retries with escalating delays.

---

## 4. GreenhouseTeams Provider — Reconcile Flow

```mermaid
flowchart TD
    START(["⚡ Reconcile triggered"])

    START --> FETCH["Fetch ASR"]
    FETCH -->|"Not found"| GONE["Return — deleted"]
    FETCH -->|"Found"| RELEVANT{"References greenhouse-teams CB?<br/><i>(CB index check)</i>"}

    RELEVANT -->|"No"| STALE_CHECK{"Has our finalizer?"}
    STALE_CHECK -->|"No"| SKIP["Return OK"]
    STALE_CHECK -->|"Yes"| STALE_CLEAN["Clean up Teams + TRBs, remove finalizer"]
    STALE_CLEAN --> SKIP

    RELEVANT -->|"Yes"| DELETED{"DeletionTimestamp?"}
    DELETED -->|"Yes"| DEL_CLEAN["Delete all Teams + TRBs<br/>Remove finalizer → done"]

    DELETED -->|"No"| SETUP["Ensure finalizer<br/>Parse CB definition + advancedConfig"]

    SETUP --> RESOLVE_NS["Resolve target namespace(s)"]

    subgraph ns_resolution ["Namespace Resolution Priority"]
        direction LR
        P1["1️⃣ advancedConfig.targetNamespaces"]
        P2["2️⃣ CB default targetNamespace"]
        P3["3️⃣ ❌ Error (no fallback)"]
        P1 -.-> P2 -.-> P3
    end

    RESOLVE_NS --> ns_resolution
    ns_resolution --> COMPUTE["Compute desired state<br/>Parse CCRNs → resolve capabilities<br/>via capabilityMapping → TeamRoles"]

    COMPUTE --> CONFLICT{"Also references a<br/>kubernetes ConnectorBinding?"}

    subgraph modes ["Operating Modes (ADR-007)"]
        direction TB
        FULL["<b>Full Reconcile</b><br/>Create Team + TeamRoleBindings"]
        TEAM_ONLY["<b>Team-Only Mode</b><br/>Create Team only<br/>(K8s provider handles RBAC)"]
    end

    CONFLICT -->|"No"| FULL
    CONFLICT -->|"Yes"| TEAM_ONLY

    FULL --> RECONCILE_TRBS["CreateOrUpdate each TRB<br/>Delete stale TRBs"]
    TEAM_ONLY --> CLEANUP_TRBS["Delete any existing TRBs<br/>(from before conflict was detected)"]

    RECONCILE_TRBS --> STATUS
    CLEANUP_TRBS --> STATUS

    STATUS["Patch provider status via SSA<br/><i>FieldOwner: pm-controller-greenhouse-teams</i>"]
    STATUS --> DONE["✅ Done"]

    style modes fill:#e3f2fd,stroke:#1565c0
    style ns_resolution fill:#fff8e1,stroke:#f9a825
    style DONE fill:#e8f5e9,stroke:#2e7d32
```

---

## 5. Status Aggregator

The aggregator is provider-agnostic — it only reads the `status.providers` map and computes top-level conditions.

```mermaid
flowchart TD
    TRIGGER(["⚡ ASR status changed"])

    TRIGGER --> READ["Read status.providers map"]

    READ --> EMPTY{"Providers map empty?"}
    EMPTY -->|"Yes"| UNKNOWN["Ready = <b>Unknown</b><br/><i>Waiting for providers to report</i>"]

    EMPTY -->|"No"| COUNT["Count providers:<br/>total vs ready"]

    COUNT --> EVAL{"Result?"}

    EVAL -->|"All ready"| ALL_OK["Ready = <b>True</b><br/>Degraded = False"]
    EVAL -->|"Some not ready"| PARTIAL["Ready = <b>False</b><br/>Degraded = <b>True</b>"]
    EVAL -->|"None ready"| NONE["Ready = <b>False</b><br/>Degraded = False"]

    UNKNOWN --> CHANGED
    ALL_OK --> CHANGED
    PARTIAL --> CHANGED
    NONE --> CHANGED

    CHANGED{"Conditions actually changed?<br/><i>(compare type + status + reason)</i>"}
    CHANGED -->|"No"| NOOP["Skip update<br/><i>(prevents reconcile loops)</i>"]
    CHANGED -->|"Yes"| WRITE["Update ASR conditions<br/>via meta.SetStatusCondition"]
    WRITE --> DONE["✅ Done"]

    style DONE fill:#e8f5e9,stroke:#2e7d32
    style NOOP fill:#f5f5f5,stroke:#999
```

---

## 6. Status Ownership Model (SSA)

How multiple independent controllers write to the same ASR without conflicts.

```mermaid
sequenceDiagram
    participant K as Kubernetes Provider
    participant G as GreenhouseTeams Provider
    participant API as API Server
    participant A as Status Aggregator

    Note over K,A: Each provider owns its slice — zero coordination needed

    K->>API: SSA Patch status.providers["kubernetes"]<br/>FieldOwner: pm-controller-kubernetes
    G->>API: SSA Patch status.providers["greenhouse-teams"]<br/>FieldOwner: pm-controller-greenhouse-teams

    Note over API: Server tracks field ownership per key

    API-->>A: Watch: ASR status changed
    A->>API: Read full status.providers
    A->>A: Compute Ready + Degraded conditions
    A->>API: Update status.conditions<br/>(aggregator owns conditions, providers own providers map)
```
