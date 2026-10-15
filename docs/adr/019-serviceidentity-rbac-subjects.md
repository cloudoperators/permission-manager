# ADR-019: ServiceIdentity Grants via Service-Account Usernames on ASR Bindings

## Status

Draft

## Context

The Kubernetes provider grants access by binding an ApplicationSpecificRole's RBAC to a
single subject: a Kubernetes `Group` named after the ASR's `spec.name`. Every subject that
should hold an ASR's access is expected to be a member of that group, which is populated out
of band (OIDC group claims, LDAP, etc.).

This models human and group-based access well, but it cannot express access for a **named
non-group identity**. The concrete driver is CI/CD pipelines authenticating to clusters via
structured authentication (OIDC ID tokens). A Concourse pipeline, for example, presents a
token whose `username` claim maps to a stable string such as `ci1:core:openstack-neutron`.
From the target cluster's RBAC perspective this is an ordinary `User` - not a group member,
and not an in-cluster ServiceAccount. There is today no way to say "grant this ASR's access
to the user `ci1:core:openstack-neutron`" short of inventing a group of one and wiring the
identity into it externally.

A related private permission-manager deployment already models this with a minimal
`ServiceIdentity` CRD: a named identity whose `spec.name` maps directly to the authenticated
Kubernetes username, carrying a list of ASR references (`roles`) whose access the identity
should receive. This ADR ports that concept into this repository and defines how the
Kubernetes provider turns a ServiceIdentity into RBAC subjects.

### Relationship to ADR-017 and ADR-018

This ADR is about **who** an ASR's access is bound to (the subject), which is orthogonal to
**what** an ASR grants (the roles and rules), the subject of ADR-017 and ADR-018. It composes
with both:

- ADR-017 adds resource-scoped CCRN types that compile to per-ASR ClusterRoles.
- ADR-018 moves `cluster.k8s`/`namespace.k8s` off per-ASR inline rules and onto bindings
  against shared, CB-owned ClusterRoles, while the **binding** that references those roles
  stays per-ASR and `asr`-labelled.

In every one of those shapes the ASR owns at least one per-ASR binding
(ClusterRoleBinding or namespace-scoped RoleBinding). This ADR adds `User` subjects to those
per-ASR bindings. It is written against the ADR-018 end state; because ADR-017 and ADR-018
are not yet implemented, the implementation of this ADR is sequenced after them, and the
"binding is always per-ASR" invariant it relies on is the one ADR-018 establishes in its
label table.

## Decision

Introduce a `ServiceIdentity` CRD and have the Kubernetes provider attach each
ServiceIdentity's username as an additional `User` subject on the per-ASR bindings of every
ASR the ServiceIdentity references.

### 1. ServiceIdentity CRD

The CRD is ported minimally from the prior-art repository:

```yaml
apiVersion: permission-manager.cloudoperators.dev/v1alpha1
kind: ServiceIdentity
metadata:
  name: ci1-core-openstack-neutron
spec:
  name: ci1:core:openstack-neutron   # maps directly to the Kubernetes username
  description: Neutron pipeline user on Ci1
  connectorRefs:
  - sci-k8s
  roles:
  - some-asr-name
  - another-asr-name
```

- `spec.name` is the literal Kubernetes username the identity authenticates as. It is the
  value a structured-auth `claimMappings.username` expression produces (e.g.
  `"ci1:" + claims.sub.replace("/", ":")`).
- `spec.roles` is a list of ApplicationSpecificRole references, by bare name, resolved in
  the ServiceIdentity's own namespace.
- `spec.connectorRefs`, `spec.description` are carried from the prior art. See the open
  question on `connectorRefs` below.

The `approvers` and `issuer` fields from earlier drafts of the prior-art spec are **not**
ported. `approvers` is a CAM-specific concept with no meaning for a ServiceIdentity here;
`issuer` is handled entirely in the clusters' structured-auth configuration (the OIDC issuer
and claim mappings live there), not on the identity object.

### 2. Reverse-lookup model: the ASR derives subjects, the ServiceIdentity is an input

The ServiceIdentity points at ASRs; the ASR does not point back. The Kubernetes provider,
when reconciling an ASR, determines its binding subjects as:

- the existing `Group` subject named `asr.spec.name` (unchanged), plus
- one `User` subject per ServiceIdentity that lists this ASR in its `spec.roles`.

The ApplicationSpecificRole remains the **sole owner and writer** of its RBAC objects. A
ServiceIdentity owns no RBAC objects. This keeps the single-owner invariant from the existing
provider intact and means a ServiceIdentity change never races the ASR reconciler for control
of a binding.

### 3. Subjects are derived from live state every reconcile

On every ASR reconcile the provider re-derives the full subject set from a live list of
ServiceIdentities that reference the ASR, and sets the binding's `subjects` to exactly that
set. The apply path already overwrites `subjects` wholesale, so adding or removing a
ServiceIdentity is just a change to the derived list.

Correctness is therefore independent of event delivery. A ServiceIdentity watch that
re-enqueues the ASRs named in a changed (or deleted) ServiceIdentity's `spec.roles` exists to
make convergence **prompt**, but it is not what makes it **correct**: even a missed watch
event self-heals on the next reconcile of the affected ASRs, because the subject set is
recomputed from scratch each time. This mirrors how the provider already re-fetches
ConnectorBindings live each reconcile rather than trusting cached state.

### 4. Subjects are `User`, never `ServiceAccount`

A ServiceIdentity compiles to:

```yaml
- kind: User
  name: <serviceIdentity.spec.name>
  apiGroup: rbac.authorization.k8s.io
```

Kubernetes represents an identity authenticated via OIDC / structured authentication as a
`User`, regardless of whether the token originated from a ServiceAccount on some other
cluster. The in-cluster `ServiceAccount` subject kind (username
`system:serviceaccount:<namespace>:<name>`) is a different concept and is **out of scope**:

- In-cluster ServiceAccounts receive access through RBAC manifests shipped with the
  application they run, not through a central permission manager.
- A `system:serviceaccount:...` subject only resolves on the one cluster where that
  ServiceAccount object exists, which fits poorly with permission-manager's fleet-wide,
  CCRN-addressed model.

The provider therefore never emits a `ServiceAccount` subject and never parses
`spec.name` for a `system:serviceaccount:` prefix. If a future use case genuinely needs to
target an in-cluster ServiceAccount, that is a separately motivated addition, not an
overload of this field.

### 5. One binding, mixed subjects

The `User` subjects are appended to the **same** per-ASR binding that carries the `Group`
subject, not placed on a separate binding. Kubernetes bindings are designed to carry a
heterogeneous `subjects` list, the subject `kind` field already distinguishes "granted via
group" from "granted via ServiceIdentity", and auditing "who holds this ASR's access" is a
single-object read. When no ServiceIdentity references an ASR, the binding is byte-identical
to today's output.

### 6. Uniform treatment across capabilities

A ServiceIdentity user is added to whatever per-ASR binding the ASR produces, with no
capability-based special-casing. In particular, if a referenced ASR carries
`cluster.k8s` + `MANAGE` (which under ADR-018 binds the per-ASR ClusterRoleBinding directly
to the built-in `cluster-admin` ClusterRole), the ServiceIdentity user is added to that
binding and thereby granted cluster-admin, exactly as a group member of that ASR would be.

The ServiceIdentity -> ASR -> capability chain is the authorization model. A policy such as
"pipeline identities must never be cluster-admin" is an organizational guardrail on which
ASRs are allowed to exist and which identities may reference them, not a type-check buried in
the provider. Encoding it as a per-capability carve-out would invent a second policy axis
that is both ungrounded and trivially circumvented (a ConnectorBinding author can define a
non-MANAGE capability that is effectively admin).

### 7. ServiceIdentity reconciler: validation and status only

ServiceIdentity gets its own reconciler whose sole job is to validate the object and write
its own status. It **never writes RBAC**. The clean split is:

- The ServiceIdentity reconciler writes `ServiceIdentity.status`.
- The ASR reconciler writes RBAC objects and `ApplicationSpecificRole.status`.

No RBAC object is written by two controllers. The two watch directions have distinct
purposes: the ASR watches ServiceIdentities to recompute **subjects**; the ServiceIdentity
watches ASRs to recompute its own **validity**.

Porting the status subresource is an additive deviation from the status-less prior-art spec;
it is the direct consequence of surfacing reference validity (below).

### 8. Validity is reference well-formedness, not referenced-ASR health

The ServiceIdentity reconciler reports a `roles` entry as **resolved** iff an ASR with that
name exists in the ServiceIdentity's namespace **and** has at least one `provider: kubernetes`
ConnectorRef. It does **not** reflect the referenced ASR's own health onto the
ServiceIdentity:

- An ASR that exists and is a Kubernetes ASR but is itself in `InvalidConfig`, or resolves to
  zero target clusters, still counts as resolved. Those problems surface on the ASR's own
  status.

This keeps ServiceIdentity status answering one question - "do my references point at real
Kubernetes ASRs?" - and avoids cascading, flapping status that would mirror every ASR
transition onto every ServiceIdentity that references it.

Unresolved references are surfaced as a single rollup condition, not a per-reference
structured array, consistent with how every other type in this repository reports problems:

```yaml
status:
  conditions:
  - type: Ready
    status: "False"
    reason: DanglingRoleRef
    message: 'roles not found as kubernetes ASRs: some-asr-name, another-asr-name'
```

A per-reference machine-readable status array is deliberately not introduced; nothing
consumes ServiceIdentity status structurally yet, and the condition message names the failing
references, which is the whole point of surfacing them.

### 9. Cluster scope follows the referenced ASRs

A ServiceIdentity carries no cluster or issuer selector. Its user is added to the bindings on
**every** cluster the referenced ASRs target, following each ASR's existing CCRN-based cluster
resolution. The ServiceIdentity does not narrow where its linked ASRs apply.

### 10. Deletion: no ServiceIdentity finalizer needed

Deleting a ServiceIdentity requires no ServiceIdentity-specific cleanup machinery, because a
ServiceIdentity owns no remote RBAC objects. The subject removal is an ASR **update**
reconcile: the deleted ServiceIdentity drops out of the live list, the affected ASRs
recompute their subjects without it, and the apply path overwrites `subjects`.

This is robust across controller restarts and unavailable clusters precisely because the
design is level-triggered. There is no persisted "process this deletion" work item that a
restart could lose; the desired state is simply "the subjects for the ServiceIdentities that
currently exist." If a target cluster is unavailable when a ServiceIdentity is deleted and the
controller restarts before the cluster recovers, the surviving ASRs are reconciled again on
startup and periodic resync, re-derive their subjects from live state, and drop the deleted
identity once the cluster is reachable. If the ASR itself is deleted, its own
finalizer-protected delete path (`pm.cloudoperators.dev/cleanup`) tears down the whole
binding.

The residual property is latency, not correctness: in the pathological missed-event case the
stale `User` subject persists until the next reconcile of the affected ASRs. Bounding that
window is a resync-interval tuning question, not a reason to introduce a finalizer that could
wedge ServiceIdentity deletion behind a stuck ASR.

### Examples

A CI pipeline user granted a namespace-admin ASR across the clusters that ASR targets:

```yaml
apiVersion: permission-manager.cloudoperators.dev/v1alpha1
kind: ServiceIdentity
metadata:
  name: ci1-core-openstack-neutron
spec:
  name: ci1:core:openstack-neutron
  description: Neutron pipeline user on Ci1
  connectorRefs:
  - sci-k8s
  roles:
  - neutron-namespace-admin
```

The resulting namespace-scoped RoleBinding produced for `neutron-namespace-admin` carries
both subjects:

```yaml
subjects:
- kind: Group
  name: neutron-namespace-admin        # the ASR's spec.name, unchanged
  apiGroup: rbac.authorization.k8s.io
- kind: User
  name: ci1:core:openstack-neutron     # the ServiceIdentity
  apiGroup: rbac.authorization.k8s.io
```

Scoping an identity down to a single pipeline job by naming it more specifically (the
username is whatever structured auth emits):

```yaml
spec:
  name: ci1:core:openstack-neutron:eu-de-1-deploy
```

## Open questions

- **Purpose of `spec.connectorRefs` for the Kubernetes provider.** In the reverse-lookup
  model the referenced ASRs' own `connectorRefs` fully determine where RBAC lands, so the
  ServiceIdentity's own `connectorRefs` has nothing left to decide for the Kubernetes RBAC
  path. Two readings remain: it is an intersection/filter that narrows a linked ASR's
  connectors to a subset (load-bearing - would change the subject derivation), or it is
  identity-domain scoping that matters only for other providers where the identity must be
  provisioned, and is inert for Kubernetes. This ADR records the **inert-for-Kubernetes**
  behavior (the subject derivation keys off `spec.roles` only; `connectorRefs` is used only by
  the ServiceIdentity reconciler for its own validity reporting) and flags the question for the
  original CRD author. Revisit if `connectorRefs` was intended as a grant gate.

## Consequences

**Benefits:**

- Fills the gap between "group-based access" and "no way to grant a named non-group identity."
  Pipeline and other OIDC identities can hold an ASR's access directly.
- Reuses the existing per-ASR binding machinery wholesale: subjects are the only delta, and
  the apply path already overwrites them. No new RBAC object types, no new owner.
- Auditable: the exact username and the ASRs it holds are both explicit on the ServiceIdentity
  object, and the granted binding shows the `User` subject alongside the group.
- Correctness is independent of event delivery and survives restarts and transiently
  unavailable clusters, because subject derivation is level-triggered from live state.

**Trade-offs:**

- Attaching a ServiceIdentity to an ASR that resolves to `cluster.k8s` + `MANAGE` grants that
  identity cluster-admin. This is intentional and uniform, and places the responsibility for
  "which identities may reference which ASRs" at the organizational layer.
- ServiceIdentity gains a status subresource not present in the prior-art spec. This is
  additive but is a divergence from the ported CRD.
- A deleted ServiceIdentity's stale subject is removed eventually, not atomically with the
  object's deletion; the removal latency on an unavailable-then-recovered cluster is bounded by
  the reconcile/resync interval.
- The meaning of `spec.connectorRefs` for the Kubernetes provider is unresolved (see open
  questions). The recorded behavior treats it as inert for Kubernetes.

**Related:**

- Kubernetes RBAC Target Architecture - the end state ADR-017 and ADR-018 reach; this ADR adds
  the subject dimension on top of their binding model.
- ADR-017: Resource-Scoped CCRN Types - resource CCRNs produce per-ASR ClusterRoles whose
  bindings gain ServiceIdentity subjects like any other.
- ADR-018: Scope-Aware Capability Mapping - establishes that `cluster.k8s`/`namespace.k8s`
  bindings stay per-ASR and `asr`-labelled while referencing shared ClusterRoles; this ADR
  depends on that per-ASR-binding invariant and is sequenced after ADR-018's implementation.
- `internal/provider/k8s/ensure_compiled.go` - `buildClusterScopedRBAC` /
  `buildNamespaceScopedRBAC` to be extended to append `User` subjects.
- `internal/controller/asr_controller.go` - to gain a ServiceIdentity watch that re-enqueues
  referenced ASRs.
