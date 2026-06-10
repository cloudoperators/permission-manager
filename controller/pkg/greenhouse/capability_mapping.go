package greenhouse

import "fmt"

// CapabilityMapping maps abstract capability names to concrete resource kinds or API paths
// as defined in the ConnectorBinding definition's capabilityMapping field.
type CapabilityMapping map[string]string

// Resolve looks up a capability name and returns the mapped value.
// Returns an error if the capability is not found in the mapping.
func (cm CapabilityMapping) Resolve(capability string) (string, error) {
	val, ok := cm[capability]
	if !ok {
		return "", fmt.Errorf("capability %q is not mapped", capability)
	}
	return val, nil
}

// Validate checks that the capability mapping is non-nil, non-empty,
// and has no empty keys or values.
func (cm CapabilityMapping) Validate() error {
	if cm == nil {
		return fmt.Errorf("capability mapping must not be nil")
	}
	if len(cm) == 0 {
		return fmt.Errorf("capability mapping must not be empty")
	}
	for k, v := range cm {
		if k == "" {
			return fmt.Errorf("capability mapping contains an empty key")
		}
		if v == "" {
			return fmt.Errorf("capability mapping contains an empty value for key %q", k)
		}
	}
	return nil
}
