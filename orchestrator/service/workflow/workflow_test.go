package workflow

import (
	"errors"
	"os"
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

func TestBuildInputSeriesResolvesCSVWithinRoot(t *testing.T) {
	root := t.TempDir()
	exec, err := NewExecutor(root)
	if err != nil {
		t.Fatalf("NewExecutor() error = %v", err)
	}
	if err := os.Mkdir(filepath.Join(root, "data"), 0o755); err != nil {
		t.Fatalf("create data dir: %v", err)
	}
	csvPath := filepath.Join(root, "data", "samples.csv")
	if err := os.WriteFile(csvPath, []byte("time_1,rawsig\n0,1\n"), 0o644); err != nil {
		t.Fatalf("write csv: %v", err)
	}

	cfg, err := exec.buildInputSeries(workflowStep{
		InputSeries: &inputSeriesSpec{CSV: filepath.Join("data", "samples.csv")},
	})
	if err != nil {
		t.Fatalf("buildInputSeries() error = %v", err)
	}
	if cfg == nil || cfg.Config == nil || cfg.Config.CSVPath != csvPath {
		t.Fatalf("buildInputSeries() = %#v, want CSVPath %q", cfg, csvPath)
	}
}

func TestBuildInputSeriesDownloadsS3Object(t *testing.T) {
	root := t.TempDir()
	t.Setenv("S3_BUCKET", "sensor-data")
	t.Setenv("S3_ENDPOINT", "https://s3.kaizen.internal")
	t.Setenv("AWS_REGION", "eu-west-1")

	var requested s3DownloadRequest
	exec, err := NewExecutor(root, WithS3Downloader(func(request s3DownloadRequest, destination string) error {
		requested = request
		return os.WriteFile(destination, []byte("time_1,rawsig\n0,1\n"), 0o644)
	}))
	if err != nil {
		t.Fatalf("NewExecutor() error = %v", err)
	}

	cfg, err := exec.buildInputSeries(workflowStep{
		InputSeries: &inputSeriesSpec{
			S3: &s3InputSeriesSpec{Key: "acoustic/latest.csv"},
		},
	})
	if err != nil {
		t.Fatalf("buildInputSeries() error = %v", err)
	}
	if cfg == nil || cfg.Config == nil {
		t.Fatalf("buildInputSeries() = %#v, want resolved config", cfg)
	}
	if requested.Bucket != "sensor-data" || requested.Key != "acoustic/latest.csv" {
		t.Fatalf("requested = %#v, want bucket/key from workflow+env", requested)
	}
	if requested.Endpoint != "https://s3.kaizen.internal" || requested.Region != "eu-west-1" || !requested.ForcePathStyle {
		t.Fatalf("requested = %#v, want endpoint/region/path-style defaults", requested)
	}
	data, err := os.ReadFile(cfg.Config.CSVPath)
	if err != nil {
		t.Fatalf("read downloaded CSV: %v", err)
	}
	if !strings.Contains(string(data), "rawsig") {
		t.Fatalf("downloaded CSV = %q, want content written by downloader", string(data))
	}
	cfg.Cleanup()
	if _, err := os.Stat(cfg.Config.CSVPath); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("cleanup should remove %s, stat error = %v", cfg.Config.CSVPath, err)
	}
}

func TestBuildInputSeriesRejectsTraversal(t *testing.T) {
	root := t.TempDir()
	exec, err := NewExecutor(root)
	if err != nil {
		t.Fatalf("NewExecutor() error = %v", err)
	}

	_, err = exec.buildInputSeries(workflowStep{
		InputSeries: &inputSeriesSpec{CSV: filepath.Join("..", "outside.csv")},
	})
	if !errors.Is(err, ErrPathEscapesRoot) {
		t.Fatalf("buildInputSeries() error = %v, want ErrPathEscapesRoot", err)
	}
}

func TestBuildInputSeriesRejectsConflictingSources(t *testing.T) {
	root := t.TempDir()
	exec, err := NewExecutor(root)
	if err != nil {
		t.Fatalf("NewExecutor() error = %v", err)
	}

	_, err = exec.buildInputSeries(workflowStep{
		InputSeries: &inputSeriesSpec{
			CSV: "data/samples.csv",
			S3:  &s3InputSeriesSpec{Bucket: "demo", Key: "samples.csv"},
		},
	})
	if err == nil || !strings.Contains(err.Error(), "exactly one source") {
		t.Fatalf("buildInputSeries() error = %v, want conflicting source rejection", err)
	}
}

func TestBuildInputSeriesRequiresS3BucketOrEnv(t *testing.T) {
	root := t.TempDir()
	exec, err := NewExecutor(root, WithS3Downloader(func(request s3DownloadRequest, destination string) error {
		return os.WriteFile(destination, nil, 0o644)
	}))
	if err != nil {
		t.Fatalf("NewExecutor() error = %v", err)
	}

	_, err = exec.buildInputSeries(workflowStep{
		InputSeries: &inputSeriesSpec{
			S3: &s3InputSeriesSpec{Key: "samples.csv"},
		},
	})
	if err == nil || !strings.Contains(err.Error(), "S3_BUCKET") {
		t.Fatalf("buildInputSeries() error = %v, want missing bucket rejection", err)
	}
}

func TestBuildTraceConfigRequiresSignalsAndPositiveInterval(t *testing.T) {
	root := t.TempDir()
	exec, err := NewExecutor(root)
	if err != nil {
		t.Fatalf("NewExecutor() error = %v", err)
	}

	_, err = exec.buildTraceConfig(workflowStep{Trace: &traceSpec{}})
	if err == nil || !strings.Contains(err.Error(), "at least one input or output") {
		t.Fatalf("buildTraceConfig() error = %v, want signal requirement", err)
	}

	invalid := 0.0
	_, err = exec.buildTraceConfig(workflowStep{
		Trace: &traceSpec{Outputs: []string{"CIvector"}, SampleEvery: &invalid},
	})
	if err == nil || !strings.Contains(err.Error(), "sample_every must be positive") {
		t.Fatalf("buildTraceConfig() error = %v, want positive interval rejection", err)
	}
}
