package main

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// TestMainExitCodes re-runs the test binary as the gateway, since main
// calls os.Exit.
func TestMainExitCodes(t *testing.T) {
	if args := os.Getenv("TG_MAIN_ARGS"); args != "" {
		os.Args = append([]string{"temporal-gateway"}, strings.Fields(args)...)
		main()
		return
	}
	exitCode := func(args string) int {
		cmd := exec.Command(os.Args[0], "-test.run=^TestMainExitCodes$")
		cmd.Env = append(os.Environ(), "TG_MAIN_ARGS="+args)
		var exitErr *exec.ExitError
		if err := cmd.Run(); errors.As(err, &exitErr) {
			return exitErr.ExitCode()
		} else if err != nil {
			t.Fatal(err)
		}
		return 0
	}
	if code := exitCode("-config " + filepath.Join(t.TempDir(), "nope.yml")); code != 1 {
		t.Fatalf("failing run: exit code = %d, want 1", code)
	}
	if code := exitCode("-h"); code != 0 {
		t.Fatalf("-h: exit code = %d, want 0", code)
	}
}
