package daemon

import (
	"strings"
	"testing"
	"time"
)

func TestTrackInvocationCapturesLifecycleAndChildren(t *testing.T) {
	service := NewService(Config{})
	detail, err := service.TrackInvocation("ptrack", []string{"sh", "-c", "printf 'alpha\\n'; sleep 1 & child=$!; wait $child; printf 'omega\\n'"}, t.TempDir(), nil, nil)
	if err != nil {
		t.Fatalf("track invocation: %v", err)
	}
	if detail.Source.Kind != "ptrack" {
		t.Fatalf("expected ptrack source, got %q", detail.Source.Kind)
	}
	if detail.PTY.Enabled {
		t.Fatal("prototype runtime should keep PTY disabled")
	}

	deadline := time.Now().Add(3 * time.Second)
	observedChild := false
	for time.Now().Before(deadline) {
		children, err := service.ProcessChildren(detail.ID)
		if err != nil {
			t.Fatalf("process children: %v", err)
		}
		if len(children.Items) > 0 {
			observedChild = true
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	if !observedChild {
		t.Fatal("expected live child-process observation")
	}

	finished, err := service.WaitForExit(detail.ID)
	if err != nil {
		t.Fatalf("wait for exit: %v", err)
	}
	if finished.Status != "exited" {
		t.Fatalf("expected exited status, got %q", finished.Status)
	}
	if finished.Outcome != "succeeded" {
		t.Fatalf("expected succeeded outcome, got %q", finished.Outcome)
	}
	if finished.ExitCode == nil || *finished.ExitCode != 0 {
		t.Fatalf("expected exit code 0, got %v", finished.ExitCode)
	}
	if finished.CPU.SampleCount == 0 {
		t.Fatal("expected CPU samples to be recorded")
	}

	logs, err := service.ProcessLogs(detail.ID, 0, 20)
	if err != nil {
		t.Fatalf("process logs: %v", err)
	}
	var joined strings.Builder
	for _, entry := range logs.Items {
		joined.WriteString(entry.Text)
	}
	if text := joined.String(); !strings.Contains(text, "alpha") || !strings.Contains(text, "omega") {
		t.Fatalf("expected captured command output, got %q", text)
	}

	children, err := service.ProcessChildren(detail.ID)
	if err != nil {
		t.Fatalf("process children after exit: %v", err)
	}
	if len(children.Items) == 0 {
		t.Fatal("expected retained child snapshot after exit")
	}
	if children.Items[0].Status != "exited" || children.Items[0].FinishedAt == nil {
		t.Fatalf("expected exited child snapshot, got %+v", children.Items[0])
	}
}

func TestTrackInvocationRequiresPtrackPrefix(t *testing.T) {
	service := NewService(Config{})
	if _, err := service.TrackInvocation("bash", []string{"echo", "hello"}, t.TempDir(), nil, nil); err == nil {
		t.Fatal("expected non-ptrack wrapper to be rejected")
	}
}
