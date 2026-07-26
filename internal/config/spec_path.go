package config

import (
	"fmt"

	"gopkg.in/yaml.v3"
)

// APISpecPaths is one or more paths to API specification files. In YAML it
// accepts either a single scalar ("apiSpec: ./api-spec.yaml") or a list
// ("apiSpec: [./base.yaml, ./overrides.yaml]"), so existing single-file
// configs keep working unchanged. When more than one path is given, the
// gateway loads and merges them into a single API spec (see spec.Load) -
// later files take precedence over earlier ones for any operation they both
// define, so a later entry can override or extend a base spec.
type APISpecPaths []string

// UnmarshalYAML implements the scalar-or-list decoding described on
// APISpecPaths.
func (p *APISpecPaths) UnmarshalYAML(value *yaml.Node) error {
	if value.Kind == yaml.ScalarNode {
		var single string
		if err := value.Decode(&single); err != nil {
			return err
		}
		*p = APISpecPaths{single}
		return nil
	}

	var list []string
	if err := value.Decode(&list); err != nil {
		return fmt.Errorf("apiSpec: must be a string or a list of strings: %w", err)
	}
	*p = list
	return nil
}

// validate checks that at least one apiSpec path is declared and that none
// of them is blank - the key spec.Load resolves each entry as a filesystem
// path by.
func (p APISpecPaths) validate() error {
	if len(p) == 0 {
		return fmt.Errorf("apiSpec is required")
	}
	for i, path := range p {
		if path == "" {
			return fmt.Errorf("apiSpec[%d] is empty", i)
		}
	}
	return nil
}
