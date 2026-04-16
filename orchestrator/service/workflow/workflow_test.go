package workflow

import (
	"errors"
	"path/filepath"
	"strings"
	"testing"
)

func TestResolveRepoPathAllowsPathsWithinRoot(t *testing.T) {
	root := t.TempDir()
	exec, err := NewExecutor(root)
	if err != nil {
		t.Fatalf("NewExecutor() error = %v", err)
	}

	tests := []struct {
		name string
		path string
		want string
	}{
		{
			name: "relative path",
			path: filepath.Join("workflows", "demo.yaml"),
			want: filepath.Join(root, "workflows", "demo.yaml"),
		},
		{
			name: "absolute path",
			path: filepath.Join(root, "fmu", "models", "Demo.fmu"),
			want: filepath.Join(root, "fmu", "models", "Demo.fmu"),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := exec.resolveRepoPath(tt.path, "workflow")
			if err != nil {
				t.Fatalf("resolveRepoPath() error = %v", err)
			}
			if got != tt.want {
				t.Fatalf("resolveRepoPath() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestResolveRepoPathRejectsTraversal(t *testing.T) {
	root := t.TempDir()
	exec, err := NewExecutor(root)
	if err != nil {
		t.Fatalf("NewExecutor() error = %v", err)
	}

	_, err = exec.resolveRepoPath(filepath.Join("..", "outside.yaml"), "workflow")
	if !errors.Is(err, ErrPathEscapesRoot) {
		t.Fatalf("resolveRepoPath() error = %v, want ErrPathEscapesRoot", err)
	}
}

func TestResolveRepoPathRejectsAbsolutePathOutsideRoot(t *testing.T) {
	root := t.TempDir()
	exec, err := NewExecutor(root)
	if err != nil {
		t.Fatalf("NewExecutor() error = %v", err)
	}

	outside := filepath.Join(filepath.Dir(root), "outside.yaml")
	_, err = exec.resolveRepoPath(outside, "workflow")
	if !errors.Is(err, ErrPathEscapesRoot) {
		t.Fatalf("resolveRepoPath() error = %v, want ErrPathEscapesRoot", err)
	}
}

func TestEncodeScalarRejectsStrings(t *testing.T) {
	_, err := encodeScalar("demo")
	if err == nil {
		t.Fatal("encodeScalar() error = nil, want rejection for string values")
	}
	if !strings.Contains(err.Error(), "string values are not supported") {
		t.Fatalf("encodeScalar() error = %v, want unsupported string message", err)
	}
}
