package envsubst

import "testing"

func TestExpand(t *testing.T) {
	t.Setenv("ENVSUBST_TEST_VAR", "value")

	tests := []struct {
		name string
		in   string
		want string
	}{
		{"set variable", "host: ${ENVSUBST_TEST_VAR}", "host: value"},
		{"set variable ignores default", "host: ${ENVSUBST_TEST_VAR:-fallback}", "host: value"},
		{"unset variable uses default", "host: ${ENVSUBST_TEST_UNSET:-fallback}", "host: fallback"},
		{"unset variable empty default", "host: ${ENVSUBST_TEST_UNSET:-}", "host: "},
		{"no placeholders passes through", "host: literal", "host: literal"},
		{"multiple placeholders", "${ENVSUBST_TEST_VAR}-${ENVSUBST_TEST_UNSET:-fallback}", "value-fallback"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := Expand([]byte(tt.in))
			if err != nil {
				t.Fatalf("Expand(%q) returned error: %v", tt.in, err)
			}
			if string(got) != tt.want {
				t.Errorf("Expand(%q) = %q, want %q", tt.in, got, tt.want)
			}
		})
	}
}

func TestExpandMissingVariableErrors(t *testing.T) {
	_, err := Expand([]byte("host: ${ENVSUBST_TEST_DEFINITELY_UNSET}"))
	if err == nil {
		t.Fatal("expected an error for an unset variable without a default")
	}
}
