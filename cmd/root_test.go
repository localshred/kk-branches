package cmd

import "testing"

func TestNewRootUse(t *testing.T) {
	root := NewRoot()
	if root.Use != "branches" {
		t.Fatalf("Use = %q, want %q", root.Use, "branches")
	}
}

func TestNewRootRunEReturnsNotImplemented(t *testing.T) {
	root := NewRoot()
	err := root.RunE(root, nil)
	if err == nil || err.Error() != "not implemented" {
		t.Fatalf("RunE() error = %v, want %q", err, "not implemented")
	}
}

func TestExecuteReturnsOneOnError(t *testing.T) {
	if code := Execute(); code != 1 {
		t.Fatalf("Execute() = %d, want 1", code)
	}
}
