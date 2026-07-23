// Package envsubst expands environment variable references in raw config
// bytes before they're parsed as YAML, so config.yml and api-spec.yaml can
// keep environment-specific values (hosts, namespaces, credentials, task
// queues) out of the checked-in files.
package envsubst

import (
	"fmt"
	"os"
	"regexp"
)

// reference matches "${VAR}" and "${VAR:-default}".
var reference = regexp.MustCompile(`\$\{([A-Za-z_][A-Za-z0-9_]*)(:-([^}]*))?\}`)

// Expand replaces every "${VAR}" or "${VAR:-default}" reference in data with
// the named environment variable's value. If the variable is unset and no
// default is given, Expand fails: config values should either come from the
// environment or have an explicit fallback, not silently resolve to "".
func Expand(data []byte) ([]byte, error) {
	var missing error

	result := reference.ReplaceAllFunc(data, func(match []byte) []byte {
		groups := reference.FindSubmatch(match)
		name := string(groups[1])
		hasDefault := len(groups[2]) > 0

		if value, ok := os.LookupEnv(name); ok {
			return []byte(value)
		}
		if hasDefault {
			return groups[3]
		}
		if missing == nil {
			missing = fmt.Errorf("environment variable %q is not set and has no default", name)
		}
		return match
	})
	if missing != nil {
		return nil, missing
	}
	return result, nil
}
