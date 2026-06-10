// Package github provides local Kubernetes types for the GitHub provider
// used by the permission-manager system.
package github

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
)

// GroupVersion is the API group and version for GitHub provider types.
var GroupVersion = schema.GroupVersion{Group: "repo-guard.cloudoperators.dev", Version: "v1"}

// SchemeBuilder is used to add types to a scheme.
var SchemeBuilder = runtime.NewSchemeBuilder(addKnownTypes)

// AddToScheme adds the GitHub provider types to the given scheme.
var AddToScheme = SchemeBuilder.AddToScheme

func addKnownTypes(scheme *runtime.Scheme) error {
	scheme.AddKnownTypes(GroupVersion,
		&GithubTeam{},
		&GithubTeamList{},
		&GithubTeamRepository{},
		&GithubTeamRepositoryList{},
		&GithubOrganization{},
		&GithubOrganizationList{},
	)
	metav1.AddToGroupVersion(scheme, GroupVersion)
	return nil
}

// GithubTeam represents a GitHub team resource.
type GithubTeam struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   GithubTeamSpec   `json:"spec"`
	Status GithubTeamStatus `json:"status,omitempty"`
}

// GithubTeamSpec defines the desired state of a GitHub team.
type GithubTeamSpec struct {
	// TeamName is the GitHub team name.
	TeamName string `json:"teamName"`
}

// GithubTeamStatus defines the observed state of a GitHub team.
type GithubTeamStatus struct {
	// TeamStatus is the reconciliation status: "complete", "failed", or "ratelimited".
	TeamStatus string `json:"teamStatus,omitempty"`
}

// GithubTeamList contains a list of GithubTeam.
type GithubTeamList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []GithubTeam `json:"items"`
}

// GithubTeamRepository represents a GitHub team-repository permission assignment.
type GithubTeamRepository struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec GithubTeamRepositorySpec `json:"spec"`
}

// GithubTeamRepositorySpec defines the desired state of a team-repository binding.
// It aggregates multiple repositories under a single permission level.
type GithubTeamRepositorySpec struct {
	// Repositories is the list of GitHub repository names in this permission group.
	Repositories []string `json:"repositories"`
	// Permission is the access permission level (e.g., "push", "pull", "admin").
	Permission string `json:"permission"`
}

// GithubTeamRepositoryList contains a list of GithubTeamRepository.
type GithubTeamRepositoryList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []GithubTeamRepository `json:"items"`
}

// GithubOrganization represents a GitHub organization resource.
type GithubOrganization struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec GithubOrganizationSpec `json:"spec"`
}

// GithubOrganizationSpec defines the desired state of a GitHub organization.
type GithubOrganizationSpec struct {
	// DefaultPublicRepositoryTeams are teams assigned to new public repositories by default.
	DefaultPublicRepositoryTeams []GithubTeamWithPermission `json:"defaultPublicRepositoryTeams,omitempty"`
	// DefaultPrivateRepositoryTeams are teams assigned to new private repositories by default.
	DefaultPrivateRepositoryTeams []GithubTeamWithPermission `json:"defaultPrivateRepositoryTeams,omitempty"`
	// OrganizationOwnerTeams are team names whose members are organization owners.
	OrganizationOwnerTeams []string `json:"organizationOwnerTeams,omitempty"`
}

// GithubOrganizationList contains a list of GithubOrganization.
type GithubOrganizationList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []GithubOrganization `json:"items"`
}

// GithubTeamWithPermission pairs a team name with a permission level.
type GithubTeamWithPermission struct {
	// Name is the GitHub team name.
	Name string `json:"name"`
	// Permission is the access permission level.
	Permission string `json:"permission"`
}

// --- DeepCopy implementations ---

// DeepCopyObject implements runtime.Object.
func (in *GithubTeam) DeepCopyObject() runtime.Object {
	return in.DeepCopy()
}

// DeepCopy returns a deep copy of GithubTeam.
func (in *GithubTeam) DeepCopy() *GithubTeam {
	if in == nil {
		return nil
	}
	out := new(GithubTeam)
	in.DeepCopyInto(out)
	return out
}

// DeepCopyInto copies all fields into another GithubTeam.
func (in *GithubTeam) DeepCopyInto(out *GithubTeam) {
	*out = *in
	out.TypeMeta = in.TypeMeta
	in.ObjectMeta.DeepCopyInto(&out.ObjectMeta)
	out.Spec = in.Spec
	out.Status = in.Status
}

// DeepCopyObject implements runtime.Object.
func (in *GithubTeamList) DeepCopyObject() runtime.Object {
	return in.DeepCopy()
}

// DeepCopy returns a deep copy of GithubTeamList.
func (in *GithubTeamList) DeepCopy() *GithubTeamList {
	if in == nil {
		return nil
	}
	out := new(GithubTeamList)
	in.DeepCopyInto(out)
	return out
}

// DeepCopyInto copies all fields into another GithubTeamList.
func (in *GithubTeamList) DeepCopyInto(out *GithubTeamList) {
	*out = *in
	out.TypeMeta = in.TypeMeta
	in.ListMeta.DeepCopyInto(&out.ListMeta)
	if in.Items != nil {
		out.Items = make([]GithubTeam, len(in.Items))
		for i := range in.Items {
			in.Items[i].DeepCopyInto(&out.Items[i])
		}
	}
}

// DeepCopyObject implements runtime.Object.
func (in *GithubTeamRepository) DeepCopyObject() runtime.Object {
	return in.DeepCopy()
}

// DeepCopy returns a deep copy of GithubTeamRepository.
func (in *GithubTeamRepository) DeepCopy() *GithubTeamRepository {
	if in == nil {
		return nil
	}
	out := new(GithubTeamRepository)
	in.DeepCopyInto(out)
	return out
}

// DeepCopyInto copies all fields into another GithubTeamRepository.
func (in *GithubTeamRepository) DeepCopyInto(out *GithubTeamRepository) {
	*out = *in
	out.TypeMeta = in.TypeMeta
	in.ObjectMeta.DeepCopyInto(&out.ObjectMeta)
	in.Spec.DeepCopyInto(&out.Spec)
}

// DeepCopyInto copies all fields into another GithubTeamRepositorySpec.
func (in *GithubTeamRepositorySpec) DeepCopyInto(out *GithubTeamRepositorySpec) {
	*out = *in
	if in.Repositories != nil {
		out.Repositories = make([]string, len(in.Repositories))
		copy(out.Repositories, in.Repositories)
	}
}

// DeepCopyObject implements runtime.Object.
func (in *GithubTeamRepositoryList) DeepCopyObject() runtime.Object {
	return in.DeepCopy()
}

// DeepCopy returns a deep copy of GithubTeamRepositoryList.
func (in *GithubTeamRepositoryList) DeepCopy() *GithubTeamRepositoryList {
	if in == nil {
		return nil
	}
	out := new(GithubTeamRepositoryList)
	in.DeepCopyInto(out)
	return out
}

// DeepCopyInto copies all fields into another GithubTeamRepositoryList.
func (in *GithubTeamRepositoryList) DeepCopyInto(out *GithubTeamRepositoryList) {
	*out = *in
	out.TypeMeta = in.TypeMeta
	in.ListMeta.DeepCopyInto(&out.ListMeta)
	if in.Items != nil {
		out.Items = make([]GithubTeamRepository, len(in.Items))
		for i := range in.Items {
			in.Items[i].DeepCopyInto(&out.Items[i])
		}
	}
}

// DeepCopyObject implements runtime.Object.
func (in *GithubOrganization) DeepCopyObject() runtime.Object {
	return in.DeepCopy()
}

// DeepCopy returns a deep copy of GithubOrganization.
func (in *GithubOrganization) DeepCopy() *GithubOrganization {
	if in == nil {
		return nil
	}
	out := new(GithubOrganization)
	in.DeepCopyInto(out)
	return out
}

// DeepCopyInto copies all fields into another GithubOrganization.
func (in *GithubOrganization) DeepCopyInto(out *GithubOrganization) {
	*out = *in
	out.TypeMeta = in.TypeMeta
	in.ObjectMeta.DeepCopyInto(&out.ObjectMeta)
	in.Spec.DeepCopyInto(&out.Spec)
}

// DeepCopyInto copies all fields into another GithubOrganizationSpec.
func (in *GithubOrganizationSpec) DeepCopyInto(out *GithubOrganizationSpec) {
	*out = *in
	if in.DefaultPublicRepositoryTeams != nil {
		out.DefaultPublicRepositoryTeams = make([]GithubTeamWithPermission, len(in.DefaultPublicRepositoryTeams))
		copy(out.DefaultPublicRepositoryTeams, in.DefaultPublicRepositoryTeams)
	}
	if in.DefaultPrivateRepositoryTeams != nil {
		out.DefaultPrivateRepositoryTeams = make([]GithubTeamWithPermission, len(in.DefaultPrivateRepositoryTeams))
		copy(out.DefaultPrivateRepositoryTeams, in.DefaultPrivateRepositoryTeams)
	}
	if in.OrganizationOwnerTeams != nil {
		out.OrganizationOwnerTeams = make([]string, len(in.OrganizationOwnerTeams))
		copy(out.OrganizationOwnerTeams, in.OrganizationOwnerTeams)
	}
}

// DeepCopyObject implements runtime.Object.
func (in *GithubOrganizationList) DeepCopyObject() runtime.Object {
	return in.DeepCopy()
}

// DeepCopy returns a deep copy of GithubOrganizationList.
func (in *GithubOrganizationList) DeepCopy() *GithubOrganizationList {
	if in == nil {
		return nil
	}
	out := new(GithubOrganizationList)
	in.DeepCopyInto(out)
	return out
}

// DeepCopyInto copies all fields into another GithubOrganizationList.
func (in *GithubOrganizationList) DeepCopyInto(out *GithubOrganizationList) {
	*out = *in
	out.TypeMeta = in.TypeMeta
	in.ListMeta.DeepCopyInto(&out.ListMeta)
	if in.Items != nil {
		out.Items = make([]GithubOrganization, len(in.Items))
		for i := range in.Items {
			in.Items[i].DeepCopyInto(&out.Items[i])
		}
	}
}
