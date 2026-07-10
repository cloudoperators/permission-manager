# Licensed to the Apache Software Foundation (ASF) under one
# or more contributor license agreements.  See the NOTICE file
# distributed with this work for additional information
# regarding copyright ownership.  The ASF licenses this file
# to you under the Apache License, Version 2.0 (the
# "License"); you may not use this file except in compliance
# with the License.  You may obtain a copy of the License at
#
#   http://www.apache.org/licenses/LICENSE-2.0
#
# Unless required by applicable law or agreed to in writing,
# software distributed under the License is distributed on an
# "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY
# KIND, either express or implied.  See the License for the
# specific language governing permissions and limitations
# under the License.

# ==============================================================================
# Local development setup
#
# Provisions the two Kind clusters used for local end-to-end testing:
#   - greenhouse-admin  (management cluster, runs greenhouse + permission-manager)
#   - greenhouse-remote (target cluster onboarded into greenhouse)
#
# Usage:
#   make setup
# ==============================================================================

ADMIN_CLUSTER         ?= greenhouse-admin
ADMIN_CLUSTER_CONFIG  ?= hack/greenhouse-admin-cluster.yaml
REMOTE_CLUSTER        ?= greenhouse-remote
REMOTE_CLUSTER_CONFIG ?= hack/greenhouse-remote-cluster.yaml

# Files used to render the structured-auth config that the remote cluster's
# kube-apiserver mounts. Source has a <placeholder> for the admin cluster's CA;
# rendered output goes into bin/ to keep generated state out of git.
STRUCTURED_AUTH_SRC ?= hack/structured-auth.yaml
STRUCTURED_AUTH_OUT ?= $(LOCALBIN)/structured-auth.yaml
ADMIN_CA_OUT        ?= $(LOCALBIN)/greenhouse-admin-ca.crt

# kind-cluster-exists checks whether a Kind cluster with the given name exists.
# Usage: $(call kind-cluster-exists,<name>)
define kind-cluster-exists
$(shell $(KIND) get clusters 2>/dev/null | grep -qx '$(1)' && echo yes)
endef

.PHONY: setup
setup: admin-cluster admin-oidc-discovery remote-cluster flux greenhouse ## Create both Kind clusters and bootstrap the greenhouse stack.

.PHONY: flux
flux: ## Bootstrap Flux on the admin cluster.
	$(KUBECTL) --context kind-$(ADMIN_CLUSTER) apply -k hack/flux/ --server-side --force-conflicts

.PHONY: greenhouse
greenhouse: ## Apply the greenhouse stack (sources, cert-manager, greenhouse, repo-guard) to the admin cluster.
	$(KUBECTL) --context kind-$(ADMIN_CLUSTER) apply -k hack/greenhouse/ --server-side --force-conflicts

.PHONY: admin-cluster
admin-cluster: ## Create the greenhouse-admin Kind cluster if it does not already exist.
	@if [ "$(call kind-cluster-exists,$(ADMIN_CLUSTER))" = "yes" ]; then \
		echo "Kind cluster '$(ADMIN_CLUSTER)' already exists. Skipping creation."; \
	else \
		echo "Creating Kind cluster '$(ADMIN_CLUSTER)' from $(ADMIN_CLUSTER_CONFIG)..."; \
		$(KIND) create cluster --name $(ADMIN_CLUSTER) --config $(ADMIN_CLUSTER_CONFIG); \
	fi

.PHONY: remote-cluster
remote-cluster: structured-auth ## Create the greenhouse-remote Kind cluster if it does not already exist.
	@if [ "$(call kind-cluster-exists,$(REMOTE_CLUSTER))" = "yes" ]; then \
		echo "Kind cluster '$(REMOTE_CLUSTER)' already exists. Skipping creation."; \
	else \
		echo "Creating Kind cluster '$(REMOTE_CLUSTER)' from $(REMOTE_CLUSTER_CONFIG)..."; \
		$(KIND) create cluster --name $(REMOTE_CLUSTER) --config $(REMOTE_CLUSTER_CONFIG); \
	fi

# admin-ca extracts the admin cluster's kube-root-ca.crt and writes it to bin/.
# Required for rendering the remote cluster's structured-auth.yaml.
.PHONY: admin-ca
admin-ca: $(LOCALBIN) admin-cluster ## Extract the greenhouse-admin CA cert into bin/.
	$(KUBECTL) --context kind-$(ADMIN_CLUSTER) get cm kube-root-ca.crt -n default -o json \
		| jq -r '.data."ca.crt"' > $(ADMIN_CA_OUT)

# admin-oidc-discovery binds the OIDC discovery role on the admin cluster so the
# remote cluster's apiserver can fetch JWKs anonymously.
.PHONY: admin-oidc-discovery
admin-oidc-discovery: admin-cluster ## Allow anonymous OIDC discovery on the admin cluster.
	$(KUBECTL) --context kind-$(ADMIN_CLUSTER) create clusterrolebinding oidc-reviewer-binding \
		--clusterrole=system:service-account-issuer-discovery \
		--group=system:unauthenticated \
		--dry-run=client -o yaml \
		| $(KUBECTL) --context kind-$(ADMIN_CLUSTER) apply -f -

# structured-auth renders the structured-auth.yaml the remote apiserver mounts,
# substituting the admin cluster's CA cert into the <placeholder>.
.PHONY: structured-auth
structured-auth: admin-ca ## Render bin/structured-auth.yaml with the admin CA inlined.
	cp $(STRUCTURED_AUTH_SRC) $(STRUCTURED_AUTH_OUT)
	yq eval --inplace '.jwt[0].issuer.certificateAuthority = load_str("$(ADMIN_CA_OUT)")' $(STRUCTURED_AUTH_OUT)

DEMO_ORG              ?= demo
OIDC_SECRET_RENDERED  ?= $(LOCALBIN)/oidc-secret.yaml

.PHONY: demo
demo: demo-org demo-rbac demo-onboard ## Full OIDC onboarding demo: create org, apply remote RBAC, onboard cluster.

.PHONY: demo-org
demo-org: ## Apply the demo Organization to the admin cluster and wait for it to become Ready.
	$(KUBECTL) --context kind-$(ADMIN_CLUSTER) apply -f config/samples/demo/organization.yaml
	@echo "Waiting for Organization $(DEMO_ORG) to become Ready..."
	@while true; do \
		if $(KUBECTL) --context kind-$(ADMIN_CLUSTER) get organizations $(DEMO_ORG) -o json 2>/dev/null | \
			jq -e '.status.statusConditions.conditions[] | select(.type=="Ready") | select(.status=="True")' > /dev/null 2>&1; then \
			echo "Organization $(DEMO_ORG) is Ready."; \
			break; \
		fi; \
		echo "  still waiting..."; \
		sleep 5; \
	done

.PHONY: demo-rbac
demo-rbac: ## Apply ClusterRoleBinding on the remote cluster authorizing Greenhouse OIDC access.
	$(KUBECTL) --context kind-$(REMOTE_CLUSTER) apply -f config/samples/demo/remote-rbac.yaml

.PHONY: demo-onboard
demo-onboard: ## Render and apply the OIDC onboarding Secret to the admin cluster, then wait for the Cluster CR.
	@mkdir -p $(LOCALBIN)
	$(KIND) get kubeconfig --name $(REMOTE_CLUSTER) | yq '.clusters[0].cluster.certificate-authority-data' > $(LOCALBIN)/remote-ca.crt
	cp config/samples/demo/oidc-secret.yaml $(OIDC_SECRET_RENDERED)
	yq eval --inplace '.stringData["ca.crt"] = load_str("$(LOCALBIN)/remote-ca.crt")' $(OIDC_SECRET_RENDERED)
	$(KUBECTL) --context kind-$(ADMIN_CLUSTER) apply -f $(OIDC_SECRET_RENDERED)
	@echo "Waiting for Cluster kind-$(REMOTE_CLUSTER) to become Ready..."
	@while true; do \
		if $(KUBECTL) --context kind-$(ADMIN_CLUSTER) get cluster kind-$(REMOTE_CLUSTER) -n $(DEMO_ORG) -o json 2>/dev/null | \
			jq -e '.status.statusConditions.conditions[] | select(.type=="Ready") | select(.status=="True")' > /dev/null 2>&1; then \
			echo "Cluster kind-$(REMOTE_CLUSTER) is Ready."; \
			break; \
		fi; \
		echo "  still waiting..."; \
		sleep 5; \
	done