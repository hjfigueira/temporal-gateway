package dotenv

import (
	"os"
	"path/filepath"
	"testing"
)

func writeEnvFile(t *testing.T, contents string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), ".env")
	if err := os.WriteFile(path, []byte(contents), 0o644); err != nil {
		t.Fatalf("write env file: %v", err)
	}
	return path
}

func TestLoadSetsUnsetVariables(t *testing.T) {
	path := writeEnvFile(t, "DOTENV_TEST_A=hello\nDOTENV_TEST_B=\"quoted value\"\n")

	if err := Load(path); err != nil {
		t.Fatalf("Load returned error: %v", err)
	}
	t.Cleanup(func() {
		os.Unsetenv("DOTENV_TEST_A")
		os.Unsetenv("DOTENV_TEST_B")
	})

	if got := os.Getenv("DOTENV_TEST_A"); got != "hello" {
		t.Errorf("DOTENV_TEST_A = %q, want %q", got, "hello")
	}
	if got := os.Getenv("DOTENV_TEST_B"); got != "quoted value" {
		t.Errorf("DOTENV_TEST_B = %q, want %q", got, "quoted value")
	}
}

func TestLoadSkipsBlankLinesAndComments(t *testing.T) {
	path := writeEnvFile(t, "\n# a comment\nDOTENV_TEST_C=value\n")

	if err := Load(path); err != nil {
		t.Fatalf("Load returned error: %v", err)
	}
	t.Cleanup(func() { os.Unsetenv("DOTENV_TEST_C") })

	if got := os.Getenv("DOTENV_TEST_C"); got != "value" {
		t.Errorf("DOTENV_TEST_C = %q, want %q", got, "value")
	}
}

func TestLoadDoesNotOverrideExistingEnv(t *testing.T) {
	t.Setenv("DOTENV_TEST_D", "from-real-env")
	path := writeEnvFile(t, "DOTENV_TEST_D=from-file\n")

	if err := Load(path); err != nil {
		t.Fatalf("Load returned error: %v", err)
	}

	if got := os.Getenv("DOTENV_TEST_D"); got != "from-real-env" {
		t.Errorf("DOTENV_TEST_D = %q, want %q (real env should win)", got, "from-real-env")
	}
}

func TestLoadMissingFileIsNotAnError(t *testing.T) {
	if err := Load(filepath.Join(t.TempDir(), "does-not-exist.env")); err != nil {
		t.Errorf("Load of a missing file returned error: %v", err)
	}
}

func TestLoadRejectsMalformedLine(t *testing.T) {
	path := writeEnvFile(t, "not-a-valid-line\n")

	if err := Load(path); err == nil {
		t.Error("expected an error for a line without '='")
	}
}
