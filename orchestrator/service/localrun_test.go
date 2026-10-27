package service

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

type fakeLocalExec struct {
	mu      sync.Mutex
	calls   []LocalCommand
	release chan struct{}
	stdout  string
	stderr  string
	err     error
}

func (f *fakeLocalExec) run(_ context.Context, cmd LocalCommand) ([]byte, []byte, error) {
	f.mu.Lock()
	f.calls = append(f.calls, cmd)
	f.mu.Unlock()
	if f.release != nil {
		<-f.release
	}
	return []byte(f.stdout), []byte(f.stderr), f.err
}

func (f *fakeLocalExec) last(t *testing.T) LocalCommand {
	t.Helper()
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.calls) == 0 {
		t.Fatal("no command executed")
	}
	return f.calls[len(f.calls)-1]
}

func writeLocalRunFixture(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	dir := filepath.Join(root, "workflows", "demonstrators", "cheylas")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(root, "fmu"), 0o755); err != nil {
		t.Fatal(err)
	}
	doc := "metadata:\n  site_id: cheylas\n  limits:\n    max_runtime_seconds: 120\nsteps:\n  - name: s\n    fmu: fmu/a.fmu\n"
	if err := os.WriteFile(filepath.Join(dir, "plain.yaml"), []byte(doc), 0o644); err != nil {
		t.Fatal(err)
	}
	return root
}

const localRunStdout = `{"s":{"y":2.5},"_run":{"status":"succeeded","workflow":{"path":"workflows/demonstrators/cheylas/plain.yaml"},` +
	`"data_window":{"mode":"since_previous_run","resolved":true,"source":"db","from":"2026-05-20T00:00:00Z","to":"2026-05-21T00:00:00Z","span_seconds":86400,"sim_start_time":0,"sim_stop_time":86400,"backlog_seconds":0}}}`

func envValue(env []string, key string) (string, bool) {
	for _, entry := range env {
		if strings.HasPrefix(entry, key+"=") {
			return strings.TrimPrefix(entry, key+"="), true
		}
	}
	return "", false
}

func TestLocalRunnerDirectRunAndInFlightGuard(t *testing.T) {
	root := writeLocalRunFixture(t)
	fake := &fakeLocalExec{release: make(chan struct{}), stdout: "noise\n" + localRunStdout + "\n"}
	runner := NewLocalRunner(LocalRunnerOptions{
		WorkDir:   root,
		RunnerBin: "/opt/bin/cads-workflow-runner",
		DBURL:     "postgres://cads:cads@127.0.0.1:5432/storhy",
	})
	runner.exec = fake.run

	run, err := runner.Submit("workflows/demonstrators/cheylas/plain.yaml")
	if err != nil {
		t.Fatalf("Submit() error = %v", err)
	}
	if !strings.HasPrefix(run.Name, "local-") || run.Phase != "Running" || run.Source != RunSourceLocal ||
		run.WorkflowPath != "workflows/demonstrators/cheylas/plain.yaml" || run.DeadlineSeconds != 120 {
		t.Fatalf("summary = %+v", run)
	}
	if _, err := runner.Submit("workflows/demonstrators/cheylas/plain.yaml"); err == nil ||
		!strings.Contains(err.Error(), "already has a local run in flight ("+run.Name+")") {
		t.Fatalf("second Submit() error = %v, want in-flight guard", err)
	}
	inflight := runner.InFlight()
	if len(inflight) != 1 || inflight[0].Name != run.Name || inflight[0].Phase != "Running" || inflight[0].Source != RunSourceLocal {
		t.Fatalf("InFlight() = %+v", inflight)
	}

	close(fake.release)
	runner.Wait()

	cmd := fake.last(t)
	if cmd.Name != "/opt/bin/cads-workflow-runner" || strings.Join(cmd.Args, " ") != "--json-output --workflow workflows/demonstrators/cheylas/plain.yaml" || cmd.Dir != root {
		t.Fatalf("command = %+v", cmd)
	}
	if id, _ := envValue(cmd.Env, "CADS_RUN_ID"); id != run.Name {
		t.Fatalf("CADS_RUN_ID = %q, want %q", id, run.Name)
	}
	if url, _ := envValue(cmd.Env, "CADS_DB_URL"); url != "postgres://cads:cads@127.0.0.1:5432/storhy" {
		t.Fatalf("CADS_DB_URL = %q", url)
	}

	if len(runner.InFlight()) != 0 {
		t.Fatalf("InFlight() after finish = %+v", runner.InFlight())
	}
	recent := runner.Recent()
	if len(recent) != 1 || recent[0].Name != run.Name || recent[0].Phase != "Succeeded" || recent[0].FinishedAt == nil {
		t.Fatalf("Recent() = %+v", recent)
	}
	if recent[0].DataWindow == nil || recent[0].DataWindow.From != "2026-05-20T00:00:00Z" {
		t.Fatalf("data window = %+v", recent[0].DataWindow)
	}
	_, results, inFlight, ok := runner.lookup(run.Name)
	if !ok || inFlight || results["s"]["y"] != 2.5 {
		t.Fatalf("lookup = %v %v %v", results, inFlight, ok)
	}

	// The guard is released: the same workflow can run again.
	if _, err := runner.Submit("workflows/demonstrators/cheylas/plain.yaml"); err != nil {
		t.Fatalf("Submit() after finish error = %v", err)
	}
	runner.Wait()
}

func TestLocalRunnerContainerCommand(t *testing.T) {
	root := writeLocalRunFixture(t)
	cases := []struct {
		tool, override, want string
	}{
		{tool: "podman", want: "postgres://cads:cads@host.containers.internal:5432/storhy"},
		{tool: "/usr/local/bin/docker", want: "postgres://cads:cads@host.docker.internal:5432/storhy"},
		{tool: "podman", override: "postgres://u:p@db.example:5432/storhy", want: "postgres://u:p@db.example:5432/storhy"},
	}
	for _, tc := range cases {
		fake := &fakeLocalExec{stdout: localRunStdout}
		runner := NewLocalRunner(LocalRunnerOptions{
			WorkDir:        root,
			Image:          "localhost/cads-fmi-demo:dev-rt",
			ContainerTool:  tc.tool,
			DBURL:          "postgres://cads:cads@127.0.0.1:5432/storhy",
			ContainerDBURL: tc.override,
		})
		runner.exec = fake.run
		run, err := runner.Submit("workflows/demonstrators/cheylas/plain.yaml")
		if err != nil {
			t.Fatalf("Submit() error = %v", err)
		}
		runner.Wait()
		cmd := fake.last(t)
		want := []string{
			"run", "--rm",
			"-e", "CADS_RUN_ID=" + run.Name,
			"-e", "CADS_DB_URL=" + tc.want,
			"-v", filepath.Join(root, "workflows") + ":/app/workflows:ro",
			"localhost/cads-fmi-demo:dev-rt",
			"/app/bin/cads-workflow-runner", "--json-output", "--workflow", "workflows/demonstrators/cheylas/plain.yaml",
		}
		if cmd.Name != tc.tool || strings.Join(cmd.Args, "\x00") != strings.Join(want, "\x00") {
			t.Fatalf("command = %s %q\nwant %q", cmd.Name, cmd.Args, want)
		}
		if run.Image != "localhost/cads-fmi-demo:dev-rt" {
			t.Fatalf("summary image = %q", run.Image)
		}
	}

	// Without a database no CADS_DB_URL is passed into the container.
	fake := &fakeLocalExec{stdout: localRunStdout}
	runner := NewLocalRunner(LocalRunnerOptions{WorkDir: root, Image: "img", ContainerTool: "podman"})
	runner.exec = fake.run
	if _, err := runner.Submit("workflows/demonstrators/cheylas/plain.yaml"); err != nil {
		t.Fatal(err)
	}
	runner.Wait()
	if strings.Contains(strings.Join(fake.last(t).Args, " "), "CADS_DB_URL") {
		t.Fatalf("args = %q, want no CADS_DB_URL", fake.last(t).Args)
	}
}

func TestContainerDBURL(t *testing.T) {
	cases := map[string]string{
		"postgres://cads:cads@localhost:5432/storhy?sslmode=disable": "postgres://cads:cads@host.containers.internal:5432/storhy?sslmode=disable",
		"postgres://cads@127.0.0.1/storhy":                           "postgres://cads@host.containers.internal/storhy",
		"postgres://cads@db.internal:5432/storhy":                    "postgres://cads@db.internal:5432/storhy",
	}
	for in, want := range cases {
		if got := containerDBURL(in, "podman"); got != want {
			t.Fatalf("containerDBURL(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestLocalRunnerPhases(t *testing.T) {
	root := writeLocalRunFixture(t)
	cases := []struct {
		name, stdout string
		err          error
		phase        string
		message      string
	}{
		{name: "skipped", stdout: `{"s":{},"_run":{"status":"skipped","error":"no new data"}}`, phase: "Succeeded"},
		{name: "failed", stdout: `{"s":{},"_run":{"status":"failed","error":"step s: boom","failed_step":"s"}}`, err: errors.New("exit status 1"), phase: "Failed", message: "step s: boom"},
		{name: "no output", stdout: "", err: errors.New("exec: not found"), phase: "Error", message: "exec: not found"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			fake := &fakeLocalExec{stdout: tc.stdout, stderr: "line1\nline2\n", err: tc.err}
			runner := NewLocalRunner(LocalRunnerOptions{WorkDir: root, RunnerBin: "runner"})
			runner.exec = fake.run
			fixed := time.Date(2026, 10, 9, 12, 0, 0, 0, time.UTC)
			runner.now = func() time.Time { return fixed }
			if _, err := runner.Submit("workflows/demonstrators/cheylas/plain.yaml"); err != nil {
				t.Fatal(err)
			}
			runner.Wait()
			recent := runner.Recent()
			if len(recent) != 1 || recent[0].Phase != tc.phase {
				t.Fatalf("Recent() = %+v, want phase %s", recent, tc.phase)
			}
			if tc.message != "" && recent[0].Message != tc.message {
				t.Fatalf("message = %q, want %q", recent[0].Message, tc.message)
			}
		})
	}
}
