// Package dotenv loads KEY=VALUE pairs from a .env-style file into the
// process environment, so config.yml and api-spec.yaml's "${VAR}"
// references (see internal/envsubst) can be satisfied without the caller
// having to export them by hand.
package dotenv

import (
	"bufio"
	"fmt"
	"os"
	"strings"
)

// Load reads path and calls os.Setenv for each KEY=VALUE line, skipping
// blank lines and lines starting with '#'. An already-set environment
// variable is left untouched, so real environment variables always take
// precedence over the file - matching the usual dotenv convention of the
// file supplying defaults for local development.
//
// A missing file at path is not an error: the caller is expected to treat
// it as "nothing to load" (see main.go, where --env defaults to a ".env"
// that most environments won't have).
func Load(path string) error {
	f, err := os.Open(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return fmt.Errorf("dotenv: open %q: %w", path, err)
	}
	defer f.Close()

	scanner := bufio.NewScanner(f)
	for lineNum := 1; scanner.Scan(); lineNum++ {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}

		key, value, ok := strings.Cut(line, "=")
		if !ok {
			return fmt.Errorf("dotenv: %q: line %d: missing '=': %q", path, lineNum, line)
		}
		key = strings.TrimSpace(key)
		if key == "" {
			return fmt.Errorf("dotenv: %q: line %d: empty key", path, lineNum)
		}

		value = unquote(strings.TrimSpace(value))
		if _, set := os.LookupEnv(key); set {
			continue
		}
		if err := os.Setenv(key, value); err != nil {
			return fmt.Errorf("dotenv: set %q: %w", key, err)
		}
	}
	if err := scanner.Err(); err != nil {
		return fmt.Errorf("dotenv: read %q: %w", path, err)
	}
	return nil
}

// unquote strips a single matching pair of surrounding double or single
// quotes, e.g. `"value"` or `'value'` both become value. An unquoted value
// passes through unchanged.
func unquote(value string) string {
	if len(value) < 2 {
		return value
	}
	first, last := value[0], value[len(value)-1]
	if (first == '"' && last == '"') || (first == '\'' && last == '\'') {
		return value[1 : len(value)-1]
	}
	return value
}
