# E2E Test Catalog

This document lists all 57 end-to-end test scenarios for the pm-controller, grouped by feature area. Each test maps to acceptance criteria (AC) from the provider specifications.

---

## Kubernetes Provider — Onboarding (4 tests)

| # | Test | What it verifies | AC |
|---|------|-----------------|-----|
| 1 | `TestOnboarding_NewClusterGetsRBAC` | Creating an ASR targeting a specific cluster provisions ClusterRole + ClusterRoleBinding on that cluster | Cluster-targeted ASR → RBAC provisioned |
| 2 | `TestOnboarding_ExistingASRAppliedOnJoin` | When a new Cluster CR appears, existing ASRs targeting that cluster get RBAC provisioned automatically | New cluster joins → existing ASRs applied |
| 3 | `TestOnboarding_MultipleASRsSingleCluster` | Two ASRs targeting the same cluster produce independent ClusterRoles that coexist | Multi-tenant: ASRs don't conflict |
| 4 | `TestOnboarding_ClusterWithNamespacePerms` | Namespace-scoped permissions create RoleBinding in target namespace while ClusterRole remains cluster-scoped | Namespace targeting works end-to-end |

## Kubernetes Provider — Offboarding (4 tests)

| # | Test | What it verifies | AC |
|---|------|-----------------|-----|
| 5 | `TestOffboarding_ClusterCRDeleted` | Deleting a Cluster CR removes it from the inventory; new ASRs don't target it | Cluster removal stops provisioning |
| 6 | `TestOffboarding_ClusterInvalid` | Setting KubeConfigValid=False on a Cluster removes it from the inventory | Invalid clusters are excluded |
| 7 | `TestOffboarding_OrphanDetection` | RBAC on a removed cluster is cleaned up by orphan detection | Orphan RBAC is garbage-collected |
| 8 | `TestOffboarding_ASRDelete_FinalizerCleansAll` | Deleting an ASR cleans RBAC from ALL targeted clusters before object deletion completes | Finalizer ensures complete cleanup |

## Kubernetes Provider — Wildcard Matching (8 tests)

| # | Test | What it verifies | AC |
|---|------|-----------------|-----|
| 9 | `TestWildcard_ClusterPrefixGlob` | `pm-e2e-consumer-*` expands to all 4 consumer clusters | Trailing-star cluster wildcard |
| 10 | `TestWildcard_ClusterBareWildcard` | `*` expands to all clusters in inventory | Bare wildcard = all clusters |
| 11 | `TestWildcard_NamespacePrefixGlob` | `apps-*` on consumer-1 creates RoleBindings only in apps-prod and apps-staging | Namespace prefix-glob filters correctly |
| 12 | `TestWildcard_NamespaceGardenerPrefix` | `shoot--wireapi--*` matches shoot--wireapi--prod and shoot--wireapi--staging | Double-dash patterns work |
| 13 | `TestWildcard_NamespaceBareWildcard` | Namespace `*` promotes to cluster scope → ClusterRoleBinding created | Wildcard namespace = cluster-wide |
| 14 | `TestWildcard_NamespaceKubePrefix` | `kube-*` matches kube-system, kube-public, kube-node-lease | System namespace patterns |
| 15 | `TestWildcard_NamespaceExactNotExist` | Exact reference to non-existent namespace does NOT create a RoleBinding | Non-existent namespaces are safe no-ops |
| 16 | `TestWildcard_OverlappingClusterPatterns` | Overlapping patterns (exact + glob) produce merged verbs (union) | RBAC verb merging across overlapping targets |

## Kubernetes Provider — Reconciliation (6 tests)

| # | Test | What it verifies | AC |
|---|------|-----------------|-----|
| 17 | `TestReconcile_PartialFailure_OthersProvisioned` | One unreachable cluster doesn't block RBAC on healthy clusters | Partial failure isolation |
| 18 | `TestReconcile_MultiASR_NoConflict` | 3 ASRs targeting the same cluster produce 3 independent ClusterRoles | No resource naming collisions |
| 19 | `TestReconcile_MultiASR_SameGroup` | 2 ASRs with the same spec.name produce separate ClusterRoles but bind to the same group | Subject group binding is correct |
| 20 | `TestReconcile_ConcurrentCreation` | Cluster CR and ASR created simultaneously still result in correct RBAC | Race-free event handling |
| 21 | `TestReconcile_RapidUpdates` | Updating an ASR 5 times rapidly results in the final state being correct | Last-write-wins convergence |
| 22 | `TestReconcile_DriftAllTypes` | Manually deleting/corrupting ClusterRole, CRB, and RoleBinding → all restored on next reconcile | Full drift correction |

## Kubernetes Provider — ConnectorBinding Lifecycle (5 tests)

| # | Test | What it verifies | AC |
|---|------|-----------------|-----|
| 23 | `TestConnector_NonK8sBinding_ASRSkipped` | ASR with only a non-kubernetes CB produces no RBAC anywhere | Provider routing works |
| 24 | `TestConnector_BindingAdded_ASRReconciled` | Adding a kubernetes CB triggers reconciliation of ASRs that reference it | Dynamic CB discovery |
| 25 | `TestConnector_BindingRemoved_ASRSkipped` | Removing a CB causes the ASR to be skipped on next reconcile | CB removal stops provisioning |
| 26 | `TestConnector_BindingProviderChanged` | Changing a CB's provider from kubernetes to azure stops reconciliation | Provider type change respected |
| 27 | `TestConnector_MultipleBindings_PartialMatch` | ASR with one k8s + one azure CB is still reconciled (partial match) | Multi-CB ASRs work correctly |

## Kubernetes Provider — RBAC Correctness (10 tests)

| # | Test | What it verifies | AC |
|---|------|-----------------|-----|
| 28 | `TestRBAC_ClusterScope_READ` | READ capability → ClusterRole with get/list/watch verbs; SAR confirms access | Capability-verb mapping: READ |
| 29 | `TestRBAC_NamespaceScope_WRITE` | WRITE + namespace → ClusterRole + RoleBinding; SAR confirms CREATE in namespace but NOT elsewhere | Namespace isolation |
| 30 | `TestRBAC_MixedScope` | Mixed cluster + namespace permissions → ClusterRole + CRB + RoleBindings | Mixed-scope handling |
| 31 | `TestRBAC_ASRUpdate_VerbsChange` | Changing READ → ADMIN updates ClusterRole verbs to `*` | Live capability changes applied |
| 32 | `TestRBAC_ASRUpdate_AddCluster` | Adding a second cluster to ASR provisions RBAC on the new cluster | Cluster addition reconciled |
| 33 | `TestRBAC_ASRDelete_FinalizerCleanup` | Deleting ASR removes all RBAC from all targets | Clean deletion |
| 34 | `TestRBAC_DriftCorrection` | Manually modifying ClusterRole rules → corrected on next reconcile | Drift self-healing |
| 35 | `TestRBAC_SubjectGroupMatchesSpecName` | CRB/RoleBinding subjects reference spec.name (not metadata.name) | Correct group subject |
| 36 | `TestRBAC_Labels` | All RBAC resources carry managed-by and ASR labels | Ownership labels present |
| 37 | `TestRBAC_Idempotent` | Triggering reconcile twice produces no errors and leaves resources unchanged | Idempotency |

## Kubernetes Provider — Status Reporting (8 tests)

| # | Test | What it verifies | AC |
|---|------|-----------------|-----|
| 38 | `TestStatus_AllSuccess_ReadyTrue` | All clusters provisioned → Ready=True, all targets show "Success" | Happy-path status |
| 39 | `TestStatus_PartialFailure_ReadyFalse` | One broken cluster → Ready=False, Degraded=True | Partial failure surfaced |
| 40 | `TestStatus_PropagationPerCluster` | Each cluster has an independent target status entry | Per-target granularity |
| 41 | `TestStatus_LastUpdatedTimestamp` | LastUpdated timestamp is recent (within 60s) | Timestamps are real |
| 42 | `TestStatus_UnreachableCluster` | Invalid kubeconfig → target shows "Unreachable" or "Error" | Error taxonomy correct |
| 43 | `TestStatus_RecoveryAfterError` | Fixing a broken kubeconfig → status transitions Error → Success, Ready=True | Self-healing status |
| 44 | `TestStatus_OrphansRemovedFromStatus` | Removing a cluster from ASR targets removes its status entry | Status reflects current targets |
| 45 | `TestStatus_DriftReprovisioned` | Manually deleting RBAC → next reconcile re-creates it and status returns to Success | Drift → re-provisioned |

## GitHub Provider (26 tests)

### Basic Operations
| # | Test | What it verifies | AC |
|---|------|-----------------|-----|
| 46 | `TestGithub_Basic_TeamAndRepoCreation` | One repo CCRN → GithubTeam + GithubTeamRepository CRs created | Basic provisioning |
| 47 | `TestGithub_Basic_TwoReposSameCapabilityAggregated` | Two repos, same capability → aggregated into one GithubTeamRepository | Repo aggregation |
| 48 | `TestGithub_Basic_TwoReposDifferentCapabilities` | Two repos, different capabilities → two separate GithubTeamRepository CRs | Per-capability separation |
| 49 | `TestGithub_Basic_TwoOrgs` | Permissions in two orgs → two separate GithubTeam CRs | Multi-org support |

### Wildcard / Organization Patching
| # | Test | What it verifies | AC |
|---|------|-----------------|-----|
| 50 | `TestGithub_Wildcard_PublicRepoType` | repository_type CCRN visibility=public → patches GithubOrg defaultPublicRepositoryTeams | Public repo wildcard |
| 51 | `TestGithub_Wildcard_PrivateRepoType` | repository_type CCRN visibility=private → patches GithubOrg defaultPrivateRepositoryTeams | Private repo wildcard |
| 52 | `TestGithub_Wildcard_AllVisibility` | repository_type CCRN visibility=* → patches both public and private repository teams | All-visibility wildcard |
| 53 | `TestGithub_Org_AdminCapability` | Organization CCRN + admin capability → team added to organizationOwnerTeams | Org owner mapping |
| 54 | `TestGithub_Org_NonAdminCapability` | Organization CCRN + non-admin capability → team NOT in organizationOwnerTeams | Non-admin org access |

### Updates
| # | Test | What it verifies | AC |
|---|------|-----------------|-----|
| 55 | `TestGithub_Update_AddRepoPermission` | Adding a second repo → GithubTeamRepository repos array grows | Additive updates |
| 56 | `TestGithub_Update_RemoveRepoPermission` | Removing a repo → repos array shrinks or GithubTeamRepository deleted | Subtractive updates |
| 57 | `TestGithub_Update_ChangeCapability` | Changing capability → old GithubTeamRepository updated/replaced, new one created | Capability change |

### Deletion
| # | Test | What it verifies | AC |
|---|------|-----------------|-----|
| 58 | `TestGithub_Deletion_ASRDeleteCleansUpCRs` | Deleting ASR removes all GithubTeam and GithubTeamRepository CRs | Clean deletion |
| 59 | `TestGithub_Deletion_ASRDeleteCleansUpOrgPatching` | Deleting ASR with org-level patching cleans GithubOrganization entries | Org cleanup on delete |

### ConnectorBinding Lifecycle
| # | Test | What it verifies | AC |
|---|------|-----------------|-----|
| 60 | `TestGithub_CB_NonGithubCBOnly` | ASR with only non-github CB → no GitHub CRs | Provider routing |
| 61 | `TestGithub_CB_AddGithubCBLater` | Adding GitHub CB to existing ASR → CRs appear | Dynamic CB addition |
| 62 | `TestGithub_CB_RemoveGithubCB` | Removing GitHub CB → GitHub CRs cleaned up | CB removal triggers cleanup |

### Status
| # | Test | What it verifies | AC |
|---|------|-----------------|-----|
| 63 | `TestGithub_Status_MissingOrgThenCreate` | Missing GithubOrg → Pending; create it → Success | Org dependency status |
| 64 | `TestGithub_Status_TeamComplete` | GithubTeam status "complete" → provider Success | Status mapping: complete |
| 65 | `TestGithub_Status_TeamFailed` | GithubTeam status "failed" → provider Error | Status mapping: failed |
| 66 | `TestGithub_Status_TeamRatelimited` | GithubTeam status "ratelimited" → provider Pending | Status mapping: ratelimited |
| 67 | `TestGithub_Status_UnmappedCapability` | Unmapped capability → provider not-ready | Validation surfaced in status |

### Cross-Namespace Targeting
| # | Test | What it verifies | AC |
|---|------|-----------------|-----|
| 68 | `TestGithub_Namespace_AdvancedConfigOverride` | advancedConfig.github.targetNamespace overrides default → CRs in specified namespace | Advanced config priority |
| 69 | `TestGithub_Namespace_CBDefault` | CB's defaultTargetNamespace used when no advancedConfig | CB default fallback |
| 70 | `TestGithub_Namespace_AdvancedConfigOverridesCBDefault` | advancedConfig takes priority over CB defaultTargetNamespace | Priority chain correct |

### Multi-ASR
| # | Test | What it verifies | AC |
|---|------|-----------------|-----|
| 71 | `TestGithub_MultiASR_SameOrgSeparateCRs` | Two ASRs targeting same org → separate GithubTeam CRs, independent org annotations | Multi-tenant isolation |

## GreenhouseTeams Provider (19 tests)

### Basic Operations
| # | Test | What it verifies | AC |
|---|------|-----------------|-----|
| 72 | `TestGreenhouseTeams_Basic_TeamAndTRBCreation` | One cluster CCRN → Team + TeamRoleBinding CRs appear | Basic provisioning |
| 73 | `TestGreenhouseTeams_Basic_CustomTeamName` | advancedConfig.teamName overrides default team name | Custom team naming |
| 74 | `TestGreenhouseTeams_Basic_DerivedTeamName` | Without teamName override, Team name derived from ASR spec.name | Default name derivation |
| 75 | `TestGreenhouseTeams_Basic_TwoCapsOneCluster` | Two capabilities on same cluster → two TRBs (one per teamRole) | Multi-capability |
| 76 | `TestGreenhouseTeams_Basic_TwoDifferentClusters` | Permissions on two clusters → two TRBs with different clusterSelector | Multi-cluster |
| 77 | `TestGreenhouseTeams_Basic_MultipleTargetNamespaces` | CRs replicated to each targetNamespace in advancedConfig | Multi-namespace replication |
| 78 | `TestGreenhouseTeams_Basic_NamespaceScopedCCRN` | Namespace-scoped CCRN → TRB.spec.namespaces populated | Namespace-level scoping |

### Capability Mapping
| # | Test | What it verifies | AC |
|---|------|-----------------|-----|
| 79 | `TestGreenhouseTeams_CapMapping_VerifyAllMappings` | READ→cluster-viewer, WRITE→cluster-developer, ADMIN→cluster-admin correctly resolved | Full mapping validation |
| 80 | `TestGreenhouseTeams_CapMapping_UnmappedCapability` | Unmapped capability → provider status not-ready | Unmapped cap handling |

### Updates
| # | Test | What it verifies | AC |
|---|------|-----------------|-----|
| 81 | `TestGreenhouseTeams_Update_AddPermission` | Adding a second permission → new TRB appears | Additive reconciliation |
| 82 | `TestGreenhouseTeams_Update_RemovePermission` | Removing a permission → stale TRB deleted | Stale resource cleanup |

### Deletion
| # | Test | What it verifies | AC |
|---|------|-----------------|-----|
| 83 | `TestGreenhouseTeams_Deletion_CleanupAll` | Deleting ASR removes all Team and TRB CRs across all target namespaces | Complete cleanup |

### ConnectorBinding Lifecycle
| # | Test | What it verifies | AC |
|---|------|-----------------|-----|
| 84 | `TestGreenhouseTeams_CB_NonGreenhouseCBOnly` | ASR with only non-greenhouse CB → no greenhouse CRs | Provider routing |
| 85 | `TestGreenhouseTeams_CB_AddedLater` | Adding greenhouse-teams CB to existing ASR → CRs appear | Dynamic CB addition |
| 86 | `TestGreenhouseTeams_CB_Removed` | Removing greenhouse-teams CB → all greenhouse CRs cleaned up | CB removal triggers cleanup |

### Status
| # | Test | What it verifies | AC |
|---|------|-----------------|-----|
| 87 | `TestGreenhouseTeams_Status_TRBReadyTrue` | TRB Ready=True → provider reports Success for that target | Status: TRB healthy |
| 88 | `TestGreenhouseTeams_Status_TRBReadyFalse` | TRB Ready=False → provider reports Error for that target | Status: TRB unhealthy |
| 89 | `TestGreenhouseTeams_Status_TRBNonExistentCluster` | TRB targeting non-existent cluster (no conditions) → Pending status | Status: pending |

### Cross-Namespace
| # | Test | What it verifies | AC |
|---|------|-----------------|-----|
| 90 | `TestGreenhouseTeams_CrossNamespace_DefaultTargetNamespace` | CRs created in CB's defaultTargetNamespace, not ASR namespace | Cross-namespace targeting |

## Multi-Provider Integration (6 tests)

| # | Test | What it verifies | AC |
|---|------|-----------------|-----|
| 91 | `TestMultiProvider_ThreeProviderAllReportStatus` | ASR with all 3 providers → each creates its CRs, all report success | Full multi-provider |
| 92 | `TestMultiProvider_PartialFailure` | One provider fails → others unaffected, failing provider shows error | Provider isolation |
| 93 | `TestMultiProvider_ProviderRecovery` | Failed provider recovers → status transitions back to ready | Self-healing |
| 94 | `TestMultiProvider_DeletionCleansAllProviders` | Deleting multi-provider ASR → each provider cleans its own resources | Independent cleanup |
| 95 | `TestMultiProvider_ConnectorRefRemoved` | Removing one CB ref → that provider's CRs cleaned, others untouched | Partial decommission |
| 96 | `TestMultiProvider_AggregatedReadyOnlyWhenAllReady` | Ready=True only when ALL providers ready; sequential progression tested | Aggregated condition semantics |

## Provider Conflict Detection (6 tests)

| # | Test | What it verifies | AC |
|---|------|-----------------|-----|
| 97 | `TestConflict_BothProviders_TeamCreated_NoTRBs` | Both k8s + greenhouse-teams CBs → Team created, TRBs suppressed | Team-only mode activation |
| 98 | `TestConflict_BothProviders_K8sRBACNormal` | In conflict mode, kubernetes provider still provisions RBAC normally | K8s provider unaffected |
| 99 | `TestConflict_K8sAdded_TRBsRemoved` | Adding k8s CB to greenhouse-only ASR → existing TRBs removed | Dynamic conflict detection |
| 100 | `TestConflict_K8sRemoved_TRBsRecreated` | Removing k8s CB → conflict resolved, TRBs re-created | Conflict resolution |
| 101 | `TestConflict_RoundTrip_K8sAddedThenRemoved` | Full lifecycle: greenhouse-only → add k8s (conflict) → remove k8s (resolved) → final state matches initial | Round-trip correctness |
| 102 | `TestConflict_StatusReporting` | In conflict mode, both providers report status independently, both show ready | Dual status reporting |

---

## Summary by User Story

### US-1: As an operator, I want ASRs to automatically provision RBAC on target clusters
**Tests:** 1–4 (Onboarding), 28–37 (RBAC Correctness)

### US-2: As an operator, I want RBAC cleaned up when clusters leave or ASRs are deleted
**Tests:** 5–8 (Offboarding), 33, 58–59 (Deletion)

### US-3: As an operator, I want pattern-based targeting with wildcards
**Tests:** 9–16 (Wildcards), 50–52 (GitHub Wildcards)

### US-4: As an operator, I want visibility into provisioning health
**Tests:** 38–45 (Status), 63–67 (GitHub Status), 87–89 (GH-Teams Status), 96 (Aggregated)

### US-5: As an operator, I want each provider to work independently
**Tests:** 17, 91–96 (Multi-Provider), 23–27 (ConnectorBinding Lifecycle)

### US-6: As an operator, I want GitHub team/repo permissions managed declaratively
**Tests:** 46–71 (GitHub Provider)

### US-7: As an operator, I want Greenhouse Teams/TRBs managed from ASR declarations
**Tests:** 72–90 (GreenhouseTeams Provider)

### US-8: As an operator, I want no duplicate RBAC when both k8s and greenhouse-teams target the same cluster
**Tests:** 97–102 (Conflict Detection)

### US-9: As an operator, I want the controller to self-heal from drift
**Tests:** 22, 34, 45 (Drift Correction)

### US-10: As an operator, I want ASRs to support multiple simultaneous providers
**Tests:** 91–96 (Multi-Provider Integration)
