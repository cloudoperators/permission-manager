package naming

const (
	// LabelDomain is the domain prefix for permission-manager labels.
	LabelDomain = "permission-manager.cloudoperators.dev/"

	// LabelSourceASR is the fully-qualified label key for the source ASR name.
	LabelSourceASR = LabelDomain + "source-asr"

	// LabelSourceNamespace is the fully-qualified label key for the source ASR namespace.
	LabelSourceNamespace = LabelDomain + "source-namespace"

	// LabelASR is the fully-qualified label key for the ASR name (used by kubernetes provider).
	LabelASR = LabelDomain + "asr"

	// LabelManagedByKey is the standard Kubernetes managed-by label key.
	LabelManagedByKey = "app.kubernetes.io/managed-by"

	// ManagedByPMController is the managed-by value for the pm-controller.
	ManagedByPMController = "pm-controller"

	// ManagedByGithub is the managed-by value for the github provider.
	ManagedByGithub = "github"

	// ManagedByGreenhouseTeams is the managed-by value for the greenhouse-teams provider.
	ManagedByGreenhouseTeams = "greenhouse-teams"
)
