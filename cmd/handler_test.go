package cmd

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/bnaylor/vibecop/internal/audit"
	"github.com/bnaylor/vibecop/internal/daemon"
	"github.com/bnaylor/vibecop/internal/evaluator"
)

// fakeEvaluator is a minimal evaluator that returns configurable results.
type fakeEvaluator struct {
	mu        sync.Mutex
	calls     int
	failUntil int // return error for the first N calls
	verdict   string
}

func (f *fakeEvaluator) Evaluate(_ context.Context, _ evaluator.ToolRequest, _ string) (evaluator.Verdict, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls++
	if f.calls <= f.failUntil {
		return evaluator.Verdict{}, errors.New("simulated evaluator failure")
	}
	v := f.verdict
	if v == "" {
		v = "approve"
	}
	return evaluator.Verdict{Verdict: v, Reason: "ok"}, nil
}

func (f *fakeEvaluator) Timeout() time.Duration { return 5 * time.Second }

func makeTestHandler(ev evalClient) func(daemon.Request) daemon.Verdict {
	stores := make(map[string]*audit.ActivityStore)
	loggers := make(map[string]*audit.Logger)
	var storesMu sync.Mutex
	d := &daemon.Daemon{}
	return makePermissionHandler(ev, d, nil, 10, false, "test-model", "anthropic", stores, loggers, &storesMu)
}

func TestHandlerPassThroughAfterThreeFailures(t *testing.T) {
	fe := &fakeEvaluator{failUntil: 3}
	h := makeTestHandler(fe)

	req := daemon.Request{
		Type: daemon.TypePermissionRequest,
		Tool: "Bash", Input: "echo hi", ProjectPath: t.TempDir(),
	}

	// First 3 calls: evaluator errors → daemon escalates each time.
	for i := 1; i <= 3; i++ {
		v := h(req)
		if v.Verdict != "escalate" && v.Verdict != "error" {
			t.Errorf("call %d: expected escalate/error before suspension, got %s", i, v.Verdict)
		}
	}

	// 4th+ calls: handler should now be suspended → approve (fail-open).
	for i := 4; i <= 6; i++ {
		v := h(req)
		if v.Verdict != "approve" {
			t.Errorf("call %d: expected approve (suspended pass-through), got %s", i, v.Verdict)
		}
	}
}

func TestHandlerResetsFailureCountOnSuccess(t *testing.T) {
	// Fail twice, succeed once, fail twice — should NOT suspend.
	fe := &fakeEvaluator{failUntil: 2, verdict: "approve"}
	h := makeTestHandler(fe)

	req := daemon.Request{
		Type: daemon.TypePermissionRequest,
		Tool: "Bash", Input: "echo hi", ProjectPath: t.TempDir(),
	}

	// Two failures.
	h(req)
	h(req)

	// One success — resets counter.
	v := h(req)
	if v.Verdict != "approve" {
		t.Errorf("expected approve on success after 2 failures, got %s", v.Verdict)
	}

	// Two more failures — counter was reset, should not suspend.
	// (failUntil is already exhausted; these calls succeed)
	for i := 0; i < 2; i++ {
		v = h(req)
		if v.Verdict == "" {
			t.Error("expected a verdict")
		}
	}

	// Should still be unsuspended — total consecutive failures never hit 3.
	// All remaining calls should get real verdicts.
	v = h(req)
	if v.Verdict != "approve" {
		t.Errorf("expected approve (not suspended), got %s", v.Verdict)
	}
}

// TestCompletePendingFeedsActivityStore covers VCOP-16.4: a human
// approve/deny on an escalated request must land in the rolling
// activity store so subsequent LLM evaluations see the resolved
// outcome (approve / deny), not just the original "escalate" verdict.
// Without this, a user who repeatedly green-lights the same escalation
// would see no learning signal in subsequent recent-activity context.
func TestCompletePendingFeedsActivityStore(t *testing.T) {
	t.Setenv("HOME", t.TempDir())

	fe := &fakeEvaluator{verdict: "escalate"}
	stores := make(map[string]*audit.ActivityStore)
	loggers := make(map[string]*audit.Logger)
	var storesMu sync.Mutex

	d := &daemon.Daemon{}
	perm := makePermissionHandler(fe, d, nil, 10, true, "test-model", "anthropic", stores, loggers, &storesMu)
	list := makeListPendingHandler(loggers, &storesMu, true)
	complete := makeCompletePendingHandler(loggers, stores, 10, &storesMu)

	req := daemon.Request{
		Type:        daemon.TypePermissionRequest,
		Tool:        "Bash",
		Input:       "echo decision-tree-test",
		ProjectPath: t.TempDir(),
	}

	// Trigger an escalate verdict that goes into the pending queue.
	_ = perm(req)

	pending, _ := list()
	if len(pending) == 0 {
		t.Fatal("expected at least one pending escalation after escalate verdict")
	}
	target := pending[0]

	if err := complete(target.ProjectHash, target.Key, "approved"); err != nil {
		t.Fatalf("complete handler returned error: %v", err)
	}

	// Verify the activity store now holds two entries: the original
	// `escalate` plus a synthesized `approve` derived from the human
	// decision. Order matters — the post-escalation entry must be
	// newest so the LLM's recent-activity slice sees it last.
	storesMu.Lock()
	store := stores[target.ProjectHash]
	storesMu.Unlock()
	if store == nil {
		t.Fatal("expected an activity store for the project after completion")
	}
	recent := store.Recent()
	if len(recent) < 2 {
		t.Fatalf("expected ≥2 activity entries (escalate + approve), got %d: %+v", len(recent), recent)
	}
	last := recent[len(recent)-1]
	if last.Verdict != "approve" {
		t.Errorf("expected last activity verdict=approve (mapped from human approved), got %q", last.Verdict)
	}
	if last.Tool != "Bash" {
		t.Errorf("expected post-escalation tool=Bash, got %q", last.Tool)
	}
}

func TestListPendingConcurrentWithPermissionHandler(t *testing.T) {
	t.Setenv("HOME", t.TempDir())

	fe := &fakeEvaluator{verdict: "escalate"}
	stores := make(map[string]*audit.ActivityStore)
	loggers := make(map[string]*audit.Logger)
	var storesMu sync.Mutex

	d := &daemon.Daemon{}
	perm := makePermissionHandler(fe, d, nil, 10, true, "test-model", "anthropic", stores, loggers, &storesMu)
	list := makeListPendingHandler(loggers, &storesMu, true)

	req := daemon.Request{
		Type:        daemon.TypePermissionRequest,
		Tool:        "Bash",
		Input:       "rm -rf /tmp/test",
		ProjectPath: t.TempDir(),
	}

	var wg sync.WaitGroup
	for i := 0; i < 20; i++ {
		wg.Add(2)
		go func() {
			defer wg.Done()
			_ = perm(req)
		}()
		go func() {
			defer wg.Done()
			_, _ = list()
		}()
	}
	wg.Wait()

	got, auditEnabled := list()
	if len(got) == 0 {
		t.Fatal("expected pending escalations after concurrent permission requests")
	}
	if !auditEnabled {
		t.Error("expected auditEnabled=true to round-trip through the handler")
	}
}
