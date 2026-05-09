package cmd

import (
	"context"
	"fmt"
	"log"
	"os"
	"sync"
	"time"

	"github.com/bnaylor/vibecop/internal/audit"
	"github.com/bnaylor/vibecop/internal/config"
	"github.com/bnaylor/vibecop/internal/daemon"
	"github.com/bnaylor/vibecop/internal/evaluator"
	"github.com/bnaylor/vibecop/internal/telemetry"
	"github.com/spf13/cobra"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
)

// evalClient is the interface the permission handler needs from the evaluator.
type evalClient interface {
	Evaluate(ctx context.Context, req evaluator.ToolRequest, systemPrompt string) (evaluator.Verdict, error)
	Timeout() time.Duration
}

const maxConsecutiveFailures = 3

var startCmd = &cobra.Command{
	Use:   "start",
	Short: "Start the background daemon",
	Long:  "Start the vibecop daemon. Runs in the foreground; send SIGTERM or use 'vibecop stop' to shut down.",
	RunE: func(cmd *cobra.Command, args []string) error {
		cfg := VibeCopConfig()

		vibecopDir, err := config.VibecopDir()
		if err != nil {
			return err
		}
		socketPath := daemon.DefaultSocketPath(vibecopDir)

		// Init OTLP telemetry (fail-open: tp is nil when disabled or when
		// every target fails to initialise). All telemetry helpers are
		// nil-safe so the rest of the daemon is unaware of the difference.
		tpCtx := context.Background()
		tp, err := telemetry.Setup(tpCtx, cfg.Telemetry)
		if err != nil {
			log.Printf("telemetry: %v (continuing without export)", err)
		}

		d := daemon.New(socketPath, cfg)

		// Create the LLM evaluator client.
		ec := evaluator.New(
			cfg.Model.Endpoint,
			cfg.Model.APIKey,
			cfg.Model.APIFormat,
			cfg.Model.Model,
			time.Duration(cfg.Daemon.TimeoutMs)*time.Millisecond,
		)

		// Per-project activity stores and audit loggers.
		stores := make(map[string]*audit.ActivityStore)
		loggers := make(map[string]*audit.Logger)
		var storesMu sync.Mutex

		d.OnPermission(makePermissionHandler(ec, d, tp, cfg.Daemon.ActivityWindow, cfg.Daemon.AuditEnabled, cfg.Model.Model, cfg.Model.APIFormat, stores, loggers, &storesMu))
		d.OnListPending(makeListPendingHandler(loggers, &storesMu, cfg.Daemon.AuditEnabled))
		d.OnCompletePending(makeCompletePendingHandler(loggers, &storesMu))
		d.OnGetConfig(func() daemon.ConfigResponse {
			return daemon.ConfigResponse{
				Endpoint:     cfg.Model.Endpoint,
				APIFormat:    cfg.Model.APIFormat,
				Model:        cfg.Model.Model,
				TimeoutMs:    cfg.Daemon.TimeoutMs,
				AuditEnabled: cfg.Daemon.AuditEnabled,
			}
		})

		// Subscribe telemetry log exporter to daemon events. Returns a
		// WaitGroup we drain after d.Run so logs flush before SDK shutdown.
		var subWG *sync.WaitGroup
		if tp != nil {
			subWG = tp.SubscribeEvents(tpCtx, d.RegisterOTLPSubscriber())
		}

		if err := d.Start(); err != nil {
			if tp != nil {
				_ = tp.Shutdown(tpCtx)
			}
			return fmt.Errorf("daemon start: %w", err)
		}

		fmt.Fprintf(os.Stderr, "vibecop: daemon started (pid %d)\n", os.Getpid())
		runErr := d.Run()

		// Daemon has fully shut down (evtCh closed, otlpCh closed). Drain
		// the log subscriber, then flush telemetry SDK.
		if subWG != nil {
			subWG.Wait()
		}
		if tp != nil {
			shutCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			if err := tp.Shutdown(shutCtx); err != nil {
				log.Printf("telemetry: shutdown: %v", err)
			}
			cancel()
		}
		return runErr
	},
}

func makePermissionHandler(
	ec evalClient,
	d *daemon.Daemon,
	tp *telemetry.Provider,
	activityWindow int,
	auditEnabled bool,
	modelName string,
	apiFormat string,
	stores map[string]*audit.ActivityStore,
	loggers map[string]*audit.Logger,
	storesMu *sync.Mutex,
) func(daemon.Request) daemon.Verdict {
	var (
		failMu              sync.Mutex
		consecutiveFailures int
		suspended           bool
	)

	return func(req daemon.Request) daemon.Verdict {
		// Fail-open if the evaluator has had too many consecutive errors.
		failMu.Lock()
		isSuspended := suspended
		failMu.Unlock()

		if isSuspended {
			tp.RecordVerdict(context.Background(), "approve", req.Tool)
			d.EmitEvent(daemon.Event{
				Tool:      req.Tool,
				Input:     req.Input,
				Verdict:   "approve",
				Reason:    "VibeCop suspended (pass-through)",
				Timestamp: time.Now().UTC().Format(time.RFC3339),
				Level:     "warn",
				Message:   "VibeCop suspended after repeated failures — pass-through mode",
			})
			return daemon.Verdict{Verdict: "approve"}
		}

		projectHash := config.ProjectHash(req.ProjectPath)

		spanCtx, rootSpan := tp.StartPermissionSpan(context.Background(), req.Tool, projectHash)
		defer rootSpan.End()

		// Get or create per-project activity store and audit logger.
		storesMu.Lock()
		store, ok := stores[projectHash]
		if !ok {
			store = audit.NewActivityStore(projectHash, activityWindow)
			store.Load() // best-effort
			stores[projectHash] = store
		}
		logger, ok := loggers[projectHash]
		if !ok {
			logger = audit.NewLogger(projectHash, auditEnabled)
			loggers[projectHash] = logger
		}
		storesMu.Unlock()

		systemPrompt, err := evaluator.ResolvePrompt(projectHash)
		if err != nil {
			log.Printf("evaluator: prompt resolution error: %v", err)
			rootSpan.SetStatus(codes.Error, "prompt resolution failed")
			rootSpan.RecordError(err)
			tp.RecordVerdict(spanCtx, "escalate", req.Tool)
			return daemon.Verdict{
				Verdict: "escalate",
				Reason:  "VibeCop: failed to load configuration",
			}
		}

		// Build tool request with recent activity context.
		recent := store.Recent()
		toolReq := evaluator.ToolRequest{
			Tool:           req.Tool,
			Input:          req.Input,
			RecentActivity: activityEntriesToVerdicts(recent),
		}

		ctx, cancel := context.WithTimeout(spanCtx, ec.Timeout())
		defer cancel()

		evalCtx, evalSpan := tp.StartEvaluatorSpan(ctx, modelName, apiFormat)
		startTime := time.Now()
		v, evalErr := ec.Evaluate(evalCtx, toolReq, systemPrompt)
		latencyMs := time.Since(startTime).Milliseconds()
		if evalErr != nil {
			evalSpan.SetStatus(codes.Error, evalErr.Error())
			evalSpan.RecordError(evalErr)
		}
		// End() is explicit, not deferred — the span name is
		// "evaluator.llm_call" and its duration must reflect the LLM round
		// trip only, not the post-eval activity-store save, audit write,
		// EmitEvent, and metric recording that follows. A panic in Evaluate
		// would crash the goroutine; Go's default unhandled-panic behavior
		// kills the process, so any "leaked" span is freed by process
		// teardown.
		evalSpan.End()

		verdictStr := v.Verdict
		reasonStr := v.Reason
		now := time.Now().UTC()

		if evalErr != nil {
			log.Printf("evaluator: %v", evalErr)
			verdictStr = "error"
			reasonStr = fmt.Sprintf("VibeCop: evaluation error — escalated (%v)", evalErr)

			failMu.Lock()
			consecutiveFailures++
			if consecutiveFailures >= maxConsecutiveFailures {
				suspended = true
				log.Printf("evaluator: %d consecutive failures — entering pass-through mode", consecutiveFailures)
				d.EmitEvent(daemon.Event{
					Level:   "error",
					Message: fmt.Sprintf("VibeCop suspended after %d consecutive failures — run 'vibecop test' to resume", consecutiveFailures),
				})
			}
			failMu.Unlock()
		} else {
			failMu.Lock()
			consecutiveFailures = 0
			failMu.Unlock()
		}

		// Record in activity log.
		store.Append(req.Tool, req.Input, verdictStr)
		if err := store.Save(); err != nil {
			log.Printf("activity: save error: %v", err)
		}

		// Write audit record.
		lat := latencyMs
		rec := audit.AuditRecord{
			Timestamp:     now.Format(time.RFC3339),
			ToolName:      req.Tool,
			ToolInput:     req.Input,
			Verdict:       verdictStr,
			Reason:        reasonStr,
			HumanDecision: nil,
			LatencyMs:     &lat,
		}

		if verdictStr == "escalate" || verdictStr == "error" {
			if _, err := logger.WritePending(rec); err != nil {
				log.Printf("audit: write pending error: %v", err)
			}
		} else {
			if err := logger.Write(rec); err != nil {
				log.Printf("audit: write error: %v", err)
			}
		}

		// Emit event for TUI subscribers.
		d.EmitEvent(daemon.Event{
			Tool:      req.Tool,
			Input:     req.Input,
			Verdict:   verdictStr,
			Reason:    reasonStr,
			LatencyMs: latencyMs,
			Timestamp: now.Format(time.RFC3339),
		})

		// Telemetry — annotate root span and record metrics.
		rootSpan.SetAttributes(
			attribute.String("vibecop.verdict", verdictStr),
			attribute.Int64("vibecop.latency_ms", latencyMs),
		)
		if reasonStr != "" {
			rootSpan.SetAttributes(attribute.String("vibecop.reason", reasonStr))
		}
		// "escalate" deliberately does not set codes.Error: the evaluator
		// completed successfully and delegated to a human. codes.Error
		// reflects "operation failed" — operators who want to track escalates
		// in dashboards can filter on the vibecop.verdict span attribute or
		// the verdicts_total{verdict="escalate"} counter.
		if verdictStr == "deny" || verdictStr == "error" {
			rootSpan.SetStatus(codes.Error, reasonStr)
		}
		tp.RecordVerdict(spanCtx, verdictStr, req.Tool)
		tp.RecordEvaluatorLatency(spanCtx, latencyMs, verdictStr)

		return daemon.Verdict{
			Verdict: verdictStr,
			Reason:  reasonStr,
		}
	}
}

func makeListPendingHandler(
	loggers map[string]*audit.Logger,
	storesMu *sync.Mutex,
	auditEnabled bool,
) func() ([]daemon.PendingEntry, bool) {
	return func() ([]daemon.PendingEntry, bool) {
		storesMu.Lock()
		snapshot := make([]*audit.Logger, 0, len(loggers))
		for _, l := range loggers {
			snapshot = append(snapshot, l)
		}
		storesMu.Unlock()

		var out []daemon.PendingEntry
		for _, l := range snapshot {
			for _, p := range l.ListPending() {
				out = append(out, daemon.PendingEntry{
					Key:         p.Key,
					ProjectHash: p.ProjectHash,
					Timestamp:   p.Timestamp,
					Tool:        p.Tool,
					Input:       p.Input,
					Verdict:     p.Verdict,
					Reason:      p.Reason,
				})
			}
		}
		return out, auditEnabled
	}
}

func makeCompletePendingHandler(
	loggers map[string]*audit.Logger,
	storesMu *sync.Mutex,
) func(string, string, string) error {
	return func(projectHash, key, humanDecision string) error {
		storesMu.Lock()
		l, ok := loggers[projectHash]
		storesMu.Unlock()
		if !ok {
			return fmt.Errorf("no logger for project %q", projectHash)
		}
		return l.CompletePending(key, humanDecision)
	}
}

func activityEntriesToVerdicts(entries []audit.ActivityEntry) []evaluator.VerdictEntry {
	out := make([]evaluator.VerdictEntry, len(entries))
	for i, e := range entries {
		out[i] = evaluator.VerdictEntry{Tool: e.Tool, Verdict: e.Verdict}
	}
	return out
}

func init() {
	rootCmd.AddCommand(startCmd)
}
