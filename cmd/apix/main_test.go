package main

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func binaryPath(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	bin := filepath.Join(dir, "apix")
	cmd := exec.Command("go", "build", "-o", bin, ".")
	cmd.Dir, _ = filepath.Abs(".")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("build failed: %v\n%s", err, out)
	}
	return bin
}

func TestRunNonExistentFile(t *testing.T) {
	bin := binaryPath(t)
	cmd := exec.Command(bin, "invoke", "does_not_exist.yaml")
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	err := cmd.Run()
	if err == nil {
		t.Fatal("expected non-zero exit for missing file")
	}
	if exitErr, ok := err.(*exec.ExitError); ok && exitErr.ExitCode() != 2 {
		t.Fatalf("expected exit 2 for missing file, got %d", exitErr.ExitCode())
	}
}

func TestRunMissingFile(t *testing.T) {
	bin := binaryPath(t)
	cmd := exec.Command(bin, "invoke")
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	err := cmd.Run()
	if err == nil {
		t.Fatal("expected non-zero exit for missing file arg")
	}
	if exitErr, ok := err.(*exec.ExitError); ok {
		code := exitErr.ExitCode()
		if code != 1 && code != 2 {
			t.Fatalf("expected exit code 1 or 2, got %d", code)
		}
	}
}

func TestUnknownSubcommand(t *testing.T) {
	bin := binaryPath(t)
	cmd := exec.Command(bin, "bogus")
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	err := cmd.Run()
	if err == nil {
		t.Fatal("expected non-zero exit for unknown subcommand")
	}
	if exitErr, ok := err.(*exec.ExitError); ok && exitErr.ExitCode() != 2 {
		t.Fatalf("expected exit 2 for unknown subcommand, got %d", exitErr.ExitCode())
	}
}

func TestMain(m *testing.M) {
	os.Exit(m.Run())
}
