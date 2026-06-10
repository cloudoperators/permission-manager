# Reconciler Flows

Visual diagrams of the pm-controller's reconciliation logic. Each provider is an independent binary with its own reconciler loop.

> **📍 Code links**: Diagram nodes link directly to the source code that implements that step. Click any highlighted node to jump to the relevant file and line.

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

    click K8S "controller/internal/controller/kubernetes/reconciler.go" "Kubernetes reconciler entry point"
    click GH "controller/internal/controller/greenhouseteams/reconciler.go" "GreenhouseTeams reconciler entry point"
    click GIT "controller/internal/controller/github/reconciler.go" "GitHub reconciler entry point"
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

    %% Code links
    click FETCH "controller/internal/controller/kubernetes/reconciler.go#L82" "r.Get(ctx, req.NamespacedName, &asr)"
    click RELEVANT "controller/internal/controller/kubernetes/reconciler.go#L91" "r.CBIndex.HasAnyKubernetesRef(asr.Spec.ConnectorRefs)"
    click STALE "controller/internal/controller/kubernetes/reconciler.go#L94" "controllerutil.ContainsFinalizer(&asr, FinalizerKubernetes)"
    click CLEANUP_STALE "controller/internal/controller/kubernetes/reconciler.go#L96-L134" "Cleanup RBAC + remove finalizer"
    click DELETED "controller/internal/controller/kubernetes/reconciler.go#L148" "!asr.DeletionTimestamp.IsZero()"
    click FINALIZER "controller/internal/controller/kubernetes/reconciler.go#L200-L207" "controllerutil.AddFinalizer + Patch"

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

    %% Code links
    click PARSE "controller/internal/controller/kubernetes/reconciler.go#L210" "buildClusterScopes(&asr, r.ClusterInventory.MatchingNames)"
    click RESOLVE "controller/internal/controller/kubernetes/cluster_inventory.go" "ClusterInventory.MatchingNames"
    click MAP_CAPS "controller/internal/controller/kubernetes/reconciler.go#L30-L36" "CapabilityVerbs map"
    click DIFF "controller/internal/controller/kubernetes/reconcile_plan.go#L34-L49" "planReconcile(asr, scopes, prevStatuses)"
    click PLAN_OUT "controller/internal/controller/kubernetes/reconcile_plan.go#L19-L29" "ReconcilePlan struct"

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

    %% Code links
    click LOOP "controller/internal/controller/kubernetes/reconcile_plan.go#L69" "for clusterName, scope := range plan.ClustersToProvision"
    click CLIENT "controller/internal/controller/kubernetes/reconcile_plan.go#L76" "factory.ClientForCluster(ctx, clusterName)"
    click UNREACH "controller/internal/controller/kubernetes/reconcile_plan.go#L79-L86" "ClusterUnreachableError handling"
    click EXPAND "controller/internal/controller/kubernetes/reconcile_plan.go#L101-L117" "expandNamespaceWildcards"
    click BUILD_RBAC "controller/internal/controller/kubernetes/rbac_generator.go" "buildRBACPlan(plan.ASRName, plan.SpecName, scope)"
    click APPLY "controller/internal/controller/kubernetes/reconcile_plan.go#L122" "applyRBACPlan(ctx, clusterClient, rbacPlan, ...)"
    click ORPHANS "controller/internal/controller/kubernetes/reconcile_plan.go#L172" "r.Cleaner.CleanupOrphans(ctx, asr.Name, plan.OrphanClusters)"
    click CLEAN_ORPHAN "controller/internal/controller/kubernetes/orphan_cleanup.go" "CleanupOrphans implementation"
    click PATCH "controller/internal/controller/kubernetes/reconciler.go#L271-L321" "r.patchStatus(ctx, &asr, statuses)"

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

    %% Code links
    click HAS_FIN "controller/internal/controller/kubernetes/reconciler.go#L149" "controllerutil.ContainsFinalizer(&asr, FinalizerKubernetes)"
    click TARGETS "controller/internal/controller/kubernetes/reconcile_plan.go#L209-L223" "extractTargetClusterNames(asr, inventory)"
    click CLEANUP "controller/internal/controller/kubernetes/rbac_cleaner.go" "r.Cleaner.CleanupAll(ctx, asr.Name, clusters)"
    click FILTER "controller/internal/controller/kubernetes/reconciler.go#L160" "filterOrphanedClusters(skipped, r.ClusterInventory)"
    click ABANDON "controller/internal/controller/kubernetes/reconciler.go#L164" "hasAbandonAnnotation(&asr)"
    click HOLD "controller/internal/controller/kubernetes/reconciler.go#L170-L183" "Hold finalizer, patch status, emit event, requeue"
    click REMOVE_FIN "controller/internal/controller/kubernetes/reconciler.go#L189-L193" "controllerutil.RemoveFinalizer + Patch"

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

    %% Code links
    click FETCH "controller/internal/controller/greenhouseteams/reconciler.go#L43" "r.Get(ctx, req.NamespacedName, &asr)"
    click RELEVANT "controller/internal/controller/greenhouseteams/reconciler.go#L52" "r.CBIndex.HasAnyProviderRef(asr.Spec.ConnectorRefs)"
    click STALE_CHECK "controller/internal/controller/greenhouseteams/reconciler.go#L55" "controllerutil.ContainsFinalizer(&asr, FinalizerGreenhouseTeams)"
    click STALE_CLEAN "controller/internal/controller/greenhouseteams/reconciler.go#L57" "r.cleanup(ctx, &asr)"
    click DELETED "controller/internal/controller/greenhouseteams/reconciler.go#L70" "!asr.DeletionTimestamp.IsZero()"
    click DEL_CLEAN "controller/internal/controller/greenhouseteams/reconciler.go#L71-L86" "cleanup + RemoveFinalizer"
    click SETUP "controller/internal/controller/greenhouseteams/reconciler.go#L89-L126" "Finalizer + CB parse + advancedConfig"
    click RESOLVE_NS "controller/internal/controller/greenhouseteams/reconciler.go#L129" "ResolveTargetNamespaces(advCfg, cbDef)"
    click COMPUTE "controller/internal/controller/greenhouseteams/reconciler.go#L142-L143" "ComputeDesiredState(&asr, teamName, capMapping)"
    click CONFLICT "controller/internal/controller/greenhouseteams/conflict.go#L87" "DetermineMode(ctx, r.Client, &asr, log)"
    click FULL "controller/internal/controller/greenhouseteams/reconciler.go#L192-L195" "ReconcileAllTRBs"
    click TEAM_ONLY "controller/internal/controller/greenhouseteams/conflict.go#L62-L72" "ModeTeamOnly"
    click RECONCILE_TRBS "controller/internal/controller/greenhouseteams/teamrolebinding.go" "ReconcileAllTRBs"
    click CLEANUP_TRBS "controller/internal/controller/greenhouseteams/conflict.go#L106" "CleanupTRBs(ctx, c, asr, log, al)"
    click STATUS "controller/internal/controller/greenhouseteams/status.go" "PatchASRProviderStatus"

    style modes fill:#e3f2fd,stroke:#1565c0
    style ns_resolution fill:#fff8e1,stroke:#f9a825
    style DONE fill:#e8f5e9,stroke:#2e7d32
```

---

## 5. GitHub Provider — Reconcile Flow

```mermaid
flowchart TD
    START(["⚡ Reconcile triggered"])

    START --> FETCH["Fetch ASR"]
    FETCH -->|"Not found"| GONE["Return — deleted"]
    FETCH -->|"Found"| RELEVANT{"References github CB?<br/><i>(CB index check)</i>"}

    RELEVANT -->|"No"| STALE_CHECK{"Has github finalizer?"}
    STALE_CHECK -->|"No"| SKIP["Return OK"]
    STALE_CHECK -->|"Yes"| STALE_CLEAN["Delete GithubTeams + GithubTeamRepos<br/>+ Org entries, remove finalizer"]
    STALE_CLEAN --> SKIP

    RELEVANT -->|"Yes"| DELETED{"DeletionTimestamp?"}
    DELETED -->|"Yes"| DEL_CLEAN["Clean up all owned resources<br/>+ Org entries, remove finalizer"]

    DELETED -->|"No"| SETUP["Ensure finalizer<br/>Parse advancedConfig + CB definition"]

    SETUP --> RESOLVE_NS["Resolve target namespace"]
    RESOLVE_NS --> BUILD["Build desired state<br/><i>Map ASR permissions via capabilityMapping<br/>→ GithubTeam + GithubTeamRepository CRs</i>"]

    BUILD --> RECONCILE_TEAMS["Reconcile GithubTeam CRs<br/><i>CreateOrPatch + delete stale</i>"]
    RECONCILE_TEAMS --> RECONCILE_REPOS["Reconcile GithubTeamRepository CRs"]
    RECONCILE_REPOS --> RECONCILE_ORGS["Patch GithubOrganization resources<br/><i>(wildcard + org-owner CCRNs)</i>"]

    RECONCILE_ORGS --> STATUS["Compute & patch provider status"]
    STATUS --> DONE["✅ Done"]

    %% Code links
    click FETCH "controller/internal/controller/github/reconciler.go#L46" "r.Get(ctx, req.NamespacedName, &asr)"
    click RELEVANT "controller/internal/controller/github/reconciler.go#L55" "r.CBIndex.FindGithubRef(asr.Spec.ConnectorRefs)"
    click STALE_CHECK "controller/internal/controller/github/reconciler.go#L59" "controllerutil.ContainsFinalizer(&asr, FinalizerGithub)"
    click STALE_CLEAN "controller/internal/controller/github/reconciler.go#L60-L93" "Label-based cleanup + cleanupOrgEntries"
    click DELETED "controller/internal/controller/github/reconciler.go#L97" "!asr.DeletionTimestamp.IsZero()"
    click DEL_CLEAN "controller/internal/controller/github/reconciler.go#L99-L113" "cleanupOwnedResources + cleanupOrgEntries"
    click SETUP "controller/internal/controller/github/reconciler.go#L117-L137" "Finalizer + ParseAdvancedConfig + ParseConnectorDefinition"
    click RESOLVE_NS "controller/internal/controller/github/reconciler.go#L140" "ResolveTargetNamespace(advConfig, cbDef, &asr)"
    click BUILD "controller/internal/controller/github/desired_state.go" "BuildDesiredState(&asr, cbDef.CapabilityMapping)"
    click RECONCILE_TEAMS "controller/internal/controller/github/github_team.go" "ReconcileTeams(ctx, r.Client, desired.Teams, ...)"
    click RECONCILE_REPOS "controller/internal/controller/github/github_team_repo.go" "ReconcileTeamRepos(ctx, r.Client, desired.TeamRepos, ...)"
    click RECONCILE_ORGS "controller/internal/controller/github/github_org.go" "r.reconcileOrganizations(ctx, &asr, cbDef, targetNamespace)"
    click STATUS "controller/internal/controller/github/status.go" "computeProviderStatus + patchProviderStatus"

    style DONE fill:#e8f5e9,stroke:#2e7d32
```

---

## 6. Status Aggregator

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

    %% Code links
    click READ "controller/internal/controller/aggregator/reconciler.go#L51" "computeConditions(asr.Status.Providers)"
    click EMPTY "controller/internal/controller/aggregator/reconciler.go#L102" "if len(providers) == 0"
    click EVAL "controller/internal/controller/aggregator/reconciler.go#L127" "switch allReady / anyReady"
    click CHANGED "controller/internal/controller/aggregator/reconciler.go#L54" "conditionsMatch(asr.Status.Conditions, desired)"
    click NOOP "controller/internal/controller/aggregator/reconciler.go#L56" "return ctrl.Result{}, nil"
    click WRITE "controller/internal/controller/aggregator/reconciler.go#L61-L63" "meta.SetStatusCondition + Status().Update"

    style DONE fill:#e8f5e9,stroke:#2e7d32
    style NOOP fill:#f5f5f5,stroke:#999
```

---

## 7. Status Ownership Model (SSA)

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

---

## Source File Index

Quick reference for the implementation files behind each diagram:

| Diagram | Primary Source | Key Functions |
|---------|---------------|---------------|
| **K8s Provider — Stage 1** | [`kubernetes/reconciler.go`](controller/internal/controller/kubernetes/reconciler.go) | `Reconcile` (L77), `SetupWithManager` (L360) |
| **K8s Provider — Stage 2** | [`kubernetes/reconcile_plan.go`](controller/internal/controller/kubernetes/reconcile_plan.go) | `planReconcile` (L34), `ReconcilePlan` struct (L19) |
| **K8s Provider — Stage 3** | [`kubernetes/reconcile_plan.go`](controller/internal/controller/kubernetes/reconcile_plan.go) | `executeReconcilePlan` (L57) |
| **K8s Provider — RBAC gen** | [`kubernetes/rbac_generator.go`](controller/internal/controller/kubernetes/rbac_generator.go) | `buildRBACPlan`, `applyRBACPlan` |
| **K8s Provider — Deletion** | [`kubernetes/rbac_cleaner.go`](controller/internal/controller/kubernetes/rbac_cleaner.go) | `CleanupAll`, `CleanupOrphans` |
| **K8s Provider — Orphan** | [`kubernetes/orphan_cleanup.go`](controller/internal/controller/kubernetes/orphan_cleanup.go) | `filterOrphanedClusters`, orphan status |
| **GreenhouseTeams** | [`greenhouseteams/reconciler.go`](controller/internal/controller/greenhouseteams/reconciler.go) | `Reconcile` (L38) |
| **GreenhouseTeams — Conflict** | [`greenhouseteams/conflict.go`](controller/internal/controller/greenhouseteams/conflict.go) | `DetermineMode` (L87), `CleanupTRBs` (L106) |
| **GreenhouseTeams — Teams** | [`greenhouseteams/team.go`](controller/internal/controller/greenhouseteams/team.go) | `ReconcileTeam` |
| **GreenhouseTeams — TRBs** | [`greenhouseteams/teamrolebinding.go`](controller/internal/controller/greenhouseteams/teamrolebinding.go) | `ReconcileAllTRBs` |
| **GitHub** | [`github/reconciler.go`](controller/internal/controller/github/reconciler.go) | `Reconcile` (L41) |
| **GitHub — Desired State** | [`github/desired_state.go`](controller/internal/controller/github/desired_state.go) | `BuildDesiredState` |
| **GitHub — Teams** | [`github/github_team.go`](controller/internal/controller/github/github_team.go) | `ReconcileTeams` |
| **GitHub — Orgs** | [`github/github_org.go`](controller/internal/controller/github/github_org.go) | `reconcileOrganizations` |
| **Status Aggregator** | [`aggregator/reconciler.go`](controller/internal/controller/aggregator/reconciler.go) | `Reconcile` (L37), `computeConditions` (L99) |
