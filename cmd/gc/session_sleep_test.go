package main

import (
	"context"
	"testing"
	"time"

	"github.com/gastownhall/gascity/internal/beads"
	"github.com/gastownhall/gascity/internal/clock"
	"github.com/gastownhall/gascity/internal/config"
	"github.com/gastownhall/gascity/internal/runtime"
)

func boolPtr(v bool) *bool { return &v }

type routedSleepProvider struct {
	runtime.Provider
	capabilities runtime.ProviderCapabilities
	sleep        runtime.SessionSleepCapability
}

func (p routedSleepProvider) Capabilities() runtime.ProviderCapabilities {
	return p.capabilities
}

func (p routedSleepProvider) SleepCapability(string) runtime.SessionSleepCapability {
	return p.sleep
}

func startedSessionNames(sp *runtime.Fake) map[string]bool {
	started := make(map[string]bool)
	for _, call := range sp.Calls {
		if call.Method == "Start" {
			started[call.Name] = true
		}
	}
	return started
}

func TestResolveSessionSleepPolicyPrecedence(t *testing.T) {
	cfg := &config.City{
		SessionSleep: config.SessionSleepConfig{
			InteractiveResume: "60s",
		},
		Rigs: []config.Rig{{
			Name: "rig-a",
			SessionSleep: config.SessionSleepConfig{
				InteractiveResume: "5m",
			},
		}},
		Agents: []config.Agent{{
			Name:                 "worker",
			Dir:                  "rig-a",
			SleepAfterIdle:       "10s",
			SleepAfterIdleSource: "agent",
		}},
	}

	session := makeBead("b1", map[string]string{
		"template":     "rig-a/worker",
		"session_name": "worker",
	})

	policy := resolveSessionSleepPolicy(session, cfg, runtime.NewFake())
	if policy.Class != config.SessionSleepInteractiveResume {
		t.Fatalf("Class = %q, want %q", policy.Class, config.SessionSleepInteractiveResume)
	}
	if policy.Requested != "10s" || policy.Effective != "10s" {
		t.Fatalf("requested/effective = %q/%q, want 10s/10s", policy.Requested, policy.Effective)
	}
	if policy.Source != "agent" {
		t.Fatalf("Source = %q, want agent", policy.Source)
	}
}

func TestWakeReasonsInteractiveResumeGraceWindow(t *testing.T) {
	now := time.Date(2026, 3, 23, 12, 0, 0, 0, time.UTC)
	cfg := &config.City{
		SessionSleep: config.SessionSleepConfig{
			InteractiveResume: "60s",
		},
		Agents: []config.Agent{{Name: "worker"}},
	}
	session := makeBead("b1", map[string]string{
		"template":            "worker",
		"session_name":        "worker",
		"started_config_hash": "started",
		"detached_at":         now.Add(-30 * time.Second).Format(time.RFC3339),
	})

	reasons := wakeReasons(session, cfg, runtime.NewFake(), nil, nil, nil, &clock.Fake{Time: now})
	if !containsWakeReason(reasons, WakeConfig) {
		t.Fatalf("expected WakeConfig during keep-warm window, got %v", reasons)
	}

	expired := &clock.Fake{Time: now.Add(31 * time.Second)}
	reasons = wakeReasons(session, cfg, runtime.NewFake(), nil, nil, nil, expired)
	if containsWakeReason(reasons, WakeConfig) {
		t.Fatalf("did not expect WakeConfig after keep-warm expiry, got %v", reasons)
	}
}

func TestWakeReasonsNonInteractiveImmediateUsesHardWakeReasons(t *testing.T) {
	now := time.Date(2026, 3, 23, 12, 0, 0, 0, time.UTC)
	cfg := &config.City{
		SessionSleep: config.SessionSleepConfig{
			NonInteractive: "0s",
		},
		Agents: []config.Agent{{
			Name:   "worker",
			Attach: boolPtr(false),
		}},
	}
	session := makeBead("b1", map[string]string{
		"template":            "worker",
		"session_name":        "worker",
		"started_config_hash": "started",
	})

	reasons := wakeReasons(session, cfg, runtime.NewFake(), nil, nil, nil, &clock.Fake{Time: now})
	if len(reasons) != 0 {
		t.Fatalf("expected no reasons without hard wake triggers, got %v", reasons)
	}

	reasons = wakeReasons(session, cfg, runtime.NewFake(), nil, map[string]bool{"worker": true}, nil, &clock.Fake{Time: now})
	if len(reasons) != 1 || reasons[0] != WakeWork {
		t.Fatalf("expected [WakeWork], got %v", reasons)
	}

	sp := runtime.NewFake()
	sp.SetPendingInteraction("worker", &runtime.PendingInteraction{RequestID: "req-1"})
	reasons = wakeReasons(session, cfg, sp, nil, nil, nil, &clock.Fake{Time: now})
	if len(reasons) != 1 || reasons[0] != WakePending {
		t.Fatalf("expected [WakePending], got %v", reasons)
	}
}

func TestReconcileDetachedAtUsesRoutedSleepCapability(t *testing.T) {
	now := time.Date(2026, 3, 23, 12, 0, 0, 0, time.UTC)
	cfg := &config.City{
		SessionSleep: config.SessionSleepConfig{
			InteractiveResume: "60s",
		},
		Agents: []config.Agent{{Name: "worker"}},
	}
	store := beads.NewMemStore()
	session, err := store.Create(beads.Bead{
		Title:  "worker",
		Type:   sessionBeadType,
		Labels: []string{sessionBeadLabel},
		Metadata: map[string]string{
			"template":     "worker",
			"session_name": "worker",
		},
	})
	if err != nil {
		t.Fatalf("store.Create: %v", err)
	}

	base := runtime.NewFake()
	provider := routedSleepProvider{
		Provider:     base,
		capabilities: runtime.ProviderCapabilities{},
		sleep:        runtime.SessionSleepCapabilityFull,
	}
	policy := resolveSessionSleepPolicy(session, cfg, provider)
	if policy.Capability != runtime.SessionSleepCapabilityFull {
		t.Fatalf("policy capability = %q, want %q", policy.Capability, runtime.SessionSleepCapabilityFull)
	}

	reconcileDetachedAt(&session, store, policy, true, provider, &clock.Fake{Time: now})

	got, err := store.Get(session.ID)
	if err != nil {
		t.Fatalf("store.Get: %v", err)
	}
	if got.Metadata["detached_at"] == "" {
		t.Fatal("detached_at was not recorded for routed full-capability session")
	}
}

func TestReconcileSessionBeads_StartsIdleDrainAfterGrace(t *testing.T) {
	env := newReconcilerTestEnv()
	env.cfg = &config.City{
		SessionSleep: config.SessionSleepConfig{
			InteractiveResume: "60s",
		},
		Agents: []config.Agent{{Name: "worker"}},
	}
	env.addDesired("worker", "worker", true)
	session := env.createSessionBead("worker", "worker")
	ts := env.clk.Time.Add(-2 * time.Minute).UTC().Format(time.RFC3339)
	_ = env.store.SetMetadataBatch(session.ID, map[string]string{
		"last_woke_at": ts,
		"detached_at":  ts,
	})
	session.Metadata["last_woke_at"] = ts
	session.Metadata["detached_at"] = ts

	cfgNames := configuredSessionNames(env.cfg, "", env.store)
	reconcileSessionBeads(
		context.Background(), []beads.Bead{session}, env.desiredState, cfgNames, env.cfg, env.sp,
		env.store, nil, nil, nil, env.dt, map[string]int{}, "",
		nil, env.clk, env.rec, 0, 0, &env.stdout, &env.stderr,
	)

	ds := env.dt.get(session.ID)
	if ds == nil {
		t.Fatal("expected idle drain to start")
	}
	if ds.reason != "idle" {
		t.Fatalf("drain reason = %q, want idle", ds.reason)
	}
	got, err := env.store.Get(session.ID)
	if err != nil {
		t.Fatalf("store.Get: %v", err)
	}
	if got.Metadata["config_wake_suppressed"] != "true" {
		t.Fatalf("config_wake_suppressed = %q, want true", got.Metadata["config_wake_suppressed"])
	}
}

func TestReconcileSessionBeads_IdleLatchedSessionDoesNotWake(t *testing.T) {
	env := newReconcilerTestEnv()
	env.cfg = &config.City{
		SessionSleep: config.SessionSleepConfig{
			InteractiveResume: "60s",
		},
		Agents: []config.Agent{{Name: "worker"}},
	}
	env.addDesired("worker", "worker", false)
	session := env.createSessionBead("worker", "worker")
	policy := resolveSessionSleepPolicy(session, env.cfg, env.sp)
	ts := env.clk.Time.Add(-2 * time.Minute).UTC().Format(time.RFC3339)
	_ = env.store.SetMetadataBatch(session.ID, map[string]string{
		"sleep_reason":             "idle",
		"sleep_policy_fingerprint": policy.Fingerprint,
		"slept_at":                 ts,
	})
	session.Metadata["sleep_reason"] = "idle"
	session.Metadata["sleep_policy_fingerprint"] = policy.Fingerprint
	session.Metadata["slept_at"] = ts

	if got := env.reconcile([]beads.Bead{session}); got != 0 {
		t.Fatalf("planned wakes = %d, want 0", got)
	}
	if starts := startedSessionNames(env.sp); len(starts) != 0 {
		t.Fatalf("unexpected starts: %v", starts)
	}
}

func TestReconcileSessionBeads_ConfigChangeWakesIdleLatchedSession(t *testing.T) {
	env := newReconcilerTestEnv()
	oldCfg := &config.City{
		SessionSleep: config.SessionSleepConfig{
			InteractiveResume: "60s",
		},
		Agents: []config.Agent{{Name: "worker"}},
	}
	env.cfg = oldCfg
	env.addDesired("worker", "worker", false)
	session := env.createSessionBead("worker", "worker")
	oldPolicy := resolveSessionSleepPolicy(session, oldCfg, env.sp)
	ts := env.clk.Time.Add(-2 * time.Minute).UTC().Format(time.RFC3339)
	_ = env.store.SetMetadataBatch(session.ID, map[string]string{
		"sleep_reason":             "idle",
		"sleep_policy_fingerprint": oldPolicy.Fingerprint,
		"slept_at":                 ts,
	})
	session.Metadata["sleep_reason"] = "idle"
	session.Metadata["sleep_policy_fingerprint"] = oldPolicy.Fingerprint
	session.Metadata["slept_at"] = ts

	env.cfg = &config.City{Agents: []config.Agent{{Name: "worker"}}}

	if got := env.reconcile([]beads.Bead{session}); got != 1 {
		t.Fatalf("planned wakes = %d, want 1", got)
	}
	if starts := startedSessionNames(env.sp); !starts["worker"] {
		t.Fatalf("expected worker to start, got %v", starts)
	}
}

func TestReconcileSessionBeads_WakesDependenciesForHardWakeRoots(t *testing.T) {
	env := newReconcilerTestEnv()
	env.cfg = &config.City{
		SessionSleep: config.SessionSleepConfig{
			NonInteractive: "0s",
		},
		Agents: []config.Agent{
			{Name: "db", Attach: boolPtr(false)},
			{Name: "api", Attach: boolPtr(false), DependsOn: []string{"db"}},
		},
	}
	env.addDesired("db", "db", false)
	env.addDesired("api", "api", false)
	dbSession := env.createSessionBead("db", "db")
	apiSession := env.createSessionBead("api", "api")
	cfgNames := configuredSessionNames(env.cfg, "", env.store)

	got := reconcileSessionBeads(
		context.Background(),
		[]beads.Bead{dbSession, apiSession},
		env.desiredState,
		cfgNames,
		env.cfg,
		env.sp,
		env.store,
		nil,
		map[string]bool{"api": true},
		nil,
		env.dt,
		map[string]int{},
		"",
		nil,
		env.clk,
		env.rec,
		0,
		0,
		&env.stdout,
		&env.stderr,
	)
	if got != 2 {
		t.Fatalf("planned wakes = %d, want 2", got)
	}
	starts := startedSessionNames(env.sp)
	if !starts["api"] || !starts["db"] {
		t.Fatalf("expected api and db starts, got %v", starts)
	}
}

func TestComputeWakeEvaluations_ConfigWakeDoesNotPropagateDependencies(t *testing.T) {
	cfg := &config.City{
		Agents: []config.Agent{
			{Name: "db"},
			{Name: "api", DependsOn: []string{"db"}},
		},
	}
	sessions := []beads.Bead{
		makeBead("db-bead", map[string]string{
			"template":     "db",
			"session_name": "db",
		}),
		makeBead("api-bead", map[string]string{
			"template":     "api",
			"session_name": "api",
		}),
	}
	evals := computeWakeEvaluations(sessions, cfg, runtime.NewFake(), nil, nil, nil, &clock.Fake{Time: time.Now().UTC()})
	dbEval := evals["db-bead"]
	if !containsWakeReason(dbEval.Reasons, WakeConfig) {
		t.Fatalf("db reasons = %v, want WakeConfig", dbEval.Reasons)
	}
	if containsWakeReason(dbEval.Reasons, WakeDependency) {
		t.Fatalf("db reasons = %v, did not want WakeDependency from config-only wake", dbEval.Reasons)
	}
}
