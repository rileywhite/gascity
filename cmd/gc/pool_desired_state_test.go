package main

import (
	"testing"

	"github.com/gastownhall/gascity/internal/beads"
	"github.com/gastownhall/gascity/internal/config"
)

func intPtr(n int) *int { return &n }

func workBead(id, routedTo, assignee, status string, priority int) beads.Bead {
	p := priority
	return beads.Bead{
		ID:       id,
		Status:   status,
		Assignee: assignee,
		Priority: &p,
		Metadata: map[string]string{"gc.routed_to": routedTo},
	}
}

func sessionBead(id, status string) beads.Bead {
	return beads.Bead{ID: id, Status: status, Type: "session"}
}

func poolAgent(name, dir string, max *int, min int) config.Agent {
	var minPtr *int
	if min > 0 {
		minPtr = &min
	}
	return config.Agent{
		Name:              name,
		Dir:               dir,
		MaxActiveSessions: max,
		MinActiveSessions: minPtr,
		Pool:              &config.PoolConfig{Min: min, Max: -1}, // mark as pool
	}
}

func TestComputePoolDesiredStates_ResumeBeatsNew(t *testing.T) {
	cfg := &config.City{
		Agents: []config.Agent{poolAgent("claude", "rig", intPtr(2), 0)},
	}
	work := []beads.Bead{
		workBead("w1", "rig/claude", "sess-1", "in_progress", 5),
		workBead("w2", "rig/claude", "", "open", 3),
		workBead("w3", "rig/claude", "", "open", 1),
	}
	sessions := []beads.Bead{sessionBead("sess-1", "open")}

	result := ComputePoolDesiredStates(cfg, work, sessions, nil)

	if len(result) != 1 {
		t.Fatalf("len(result) = %d, want 1", len(result))
	}
	reqs := result[0].Requests
	// Max=2: resume (w1) + 1 new. The resume should come first.
	if len(reqs) != 2 {
		t.Fatalf("len(requests) = %d, want 2 (max=2)", len(reqs))
	}
	if reqs[0].Tier != "resume" {
		t.Errorf("first request tier = %q, want resume", reqs[0].Tier)
	}
	if reqs[0].SessionBeadID != "sess-1" {
		t.Errorf("first request session = %q, want sess-1", reqs[0].SessionBeadID)
	}
	if reqs[1].Tier != "new" {
		t.Errorf("second request tier = %q, want new", reqs[1].Tier)
	}
}

func TestComputePoolDesiredStates_MaxCapsTotal(t *testing.T) {
	cfg := &config.City{
		Agents: []config.Agent{poolAgent("claude", "rig", intPtr(2), 0)},
	}
	work := []beads.Bead{
		workBead("w1", "rig/claude", "", "open", 5),
		workBead("w2", "rig/claude", "", "open", 3),
		workBead("w3", "rig/claude", "", "open", 1),
	}

	result := ComputePoolDesiredStates(cfg, work, nil, nil)

	if len(result) != 1 {
		t.Fatalf("len(result) = %d, want 1", len(result))
	}
	// Max=2: only 2 of the 3 ready beads get sessions.
	if len(result[0].Requests) != 2 {
		t.Errorf("len(requests) = %d, want 2 (capped by max)", len(result[0].Requests))
	}
	// Highest priority should be accepted.
	if result[0].Requests[0].BeadPriority != 5 {
		t.Errorf("first request priority = %d, want 5", result[0].Requests[0].BeadPriority)
	}
}

func TestComputePoolDesiredStates_MaxCapsResumeBeads(t *testing.T) {
	cfg := &config.City{
		Agents: []config.Agent{poolAgent("claude", "rig", intPtr(2), 0)},
	}
	work := []beads.Bead{
		workBead("w1", "rig/claude", "s1", "in_progress", 5),
		workBead("w2", "rig/claude", "s2", "in_progress", 3),
		workBead("w3", "rig/claude", "s3", "in_progress", 1),
	}
	sessions := []beads.Bead{
		sessionBead("s1", "open"),
		sessionBead("s2", "open"),
		sessionBead("s3", "open"),
	}

	result := ComputePoolDesiredStates(cfg, work, sessions, nil)

	// Max=2: only 2 of the 3 in-progress beads get sessions.
	if len(result) != 1 {
		t.Fatalf("len(result) = %d, want 1", len(result))
	}
	if len(result[0].Requests) != 2 {
		t.Errorf("len(requests) = %d, want 2 (max caps even resume)", len(result[0].Requests))
	}
}

func TestComputePoolDesiredStates_MinFillsIdle(t *testing.T) {
	cfg := &config.City{
		Agents: []config.Agent{poolAgent("wf-ctrl", "", intPtr(1), 1)},
	}

	result := ComputePoolDesiredStates(cfg, nil, nil, nil)

	if len(result) != 1 {
		t.Fatalf("len(result) = %d, want 1", len(result))
	}
	if len(result[0].Requests) != 1 {
		t.Errorf("len(requests) = %d, want 1 (min=1 fills idle)", len(result[0].Requests))
	}
}

func TestComputePoolDesiredStates_MinRespectsMax(t *testing.T) {
	cfg := &config.City{
		Agents: []config.Agent{poolAgent("worker", "", intPtr(0), 5)},
	}

	result := ComputePoolDesiredStates(cfg, nil, nil, nil)

	// Max=0 should prevent any sessions even though min=5.
	total := 0
	for _, ds := range result {
		total += len(ds.Requests)
	}
	if total != 0 {
		t.Errorf("total requests = %d, want 0 (max=0 overrides min)", total)
	}
}

func TestComputePoolDesiredStates_WorkspaceCap(t *testing.T) {
	wsMax := 3
	cfg := &config.City{
		Workspace: config.Workspace{MaxActiveSessions: &wsMax},
		Agents: []config.Agent{
			poolAgent("claude", "rig", nil, 0),
			poolAgent("codex", "rig", nil, 0),
		},
	}
	work := []beads.Bead{
		workBead("w1", "rig/claude", "", "open", 5),
		workBead("w2", "rig/claude", "", "open", 4),
		workBead("w3", "rig/codex", "", "open", 3),
		workBead("w4", "rig/codex", "", "open", 2),
	}

	result := ComputePoolDesiredStates(cfg, work, nil, nil)

	total := 0
	for _, ds := range result {
		total += len(ds.Requests)
	}
	if total != 3 {
		t.Errorf("total requests = %d, want 3 (workspace cap)", total)
	}
}

func TestComputePoolDesiredStates_RigCap(t *testing.T) {
	rigMax := 2
	cfg := &config.City{
		Rigs: []config.Rig{{Name: "rig", Path: "/tmp/rig", MaxActiveSessions: &rigMax}},
		Agents: []config.Agent{
			poolAgent("claude", "rig", nil, 0),
			poolAgent("codex", "rig", nil, 0),
		},
	}
	work := []beads.Bead{
		workBead("w1", "rig/claude", "", "open", 5),
		workBead("w2", "rig/claude", "", "open", 4),
		workBead("w3", "rig/codex", "", "open", 3),
	}

	result := ComputePoolDesiredStates(cfg, work, nil, nil)

	total := 0
	for _, ds := range result {
		total += len(ds.Requests)
	}
	if total != 2 {
		t.Errorf("total requests = %d, want 2 (rig cap)", total)
	}
}

func TestComputePoolDesiredStates_NestedCaps(t *testing.T) {
	wsMax := 10
	rigMax := 3
	cfg := &config.City{
		Workspace: config.Workspace{MaxActiveSessions: &wsMax},
		Rigs:      []config.Rig{{Name: "rig", Path: "/tmp/rig", MaxActiveSessions: &rigMax}},
		Agents: []config.Agent{
			poolAgent("claude", "rig", intPtr(2), 0),
			poolAgent("codex", "rig", intPtr(2), 0),
		},
	}
	work := []beads.Bead{
		workBead("w1", "rig/claude", "", "open", 5),
		workBead("w2", "rig/claude", "", "open", 4),
		workBead("w3", "rig/codex", "", "open", 3),
		workBead("w4", "rig/codex", "", "open", 2),
	}

	result := ComputePoolDesiredStates(cfg, work, nil, nil)

	total := 0
	perAgent := make(map[string]int)
	for _, ds := range result {
		perAgent[ds.Template] = len(ds.Requests)
		total += len(ds.Requests)
	}
	// Rig cap=3, agent caps=2 each. 4 beads, but rig caps at 3.
	if total != 3 {
		t.Errorf("total = %d, want 3 (rig cap)", total)
	}
	// Claude gets 2 (its max), codex gets 1 (rig cap - claude's 2).
	if perAgent["rig/claude"] != 2 {
		t.Errorf("claude = %d, want 2", perAgent["rig/claude"])
	}
	if perAgent["rig/codex"] != 1 {
		t.Errorf("codex = %d, want 1", perAgent["rig/codex"])
	}
}

func TestComputePoolDesiredStates_UnlimitedWhenUnset(t *testing.T) {
	cfg := &config.City{
		Agents: []config.Agent{poolAgent("claude", "", nil, 0)},
	}
	work := []beads.Bead{
		workBead("w1", "claude", "", "open", 5),
		workBead("w2", "claude", "", "open", 4),
		workBead("w3", "claude", "", "open", 3),
		workBead("w4", "claude", "", "open", 2),
		workBead("w5", "claude", "", "open", 1),
	}

	result := ComputePoolDesiredStates(cfg, work, nil, nil)

	total := 0
	for _, ds := range result {
		total += len(ds.Requests)
	}
	if total != 5 {
		t.Errorf("total = %d, want 5 (unlimited)", total)
	}
}

func TestComputePoolDesiredStates_ClosedSessionNotResumed(t *testing.T) {
	cfg := &config.City{
		Agents: []config.Agent{poolAgent("claude", "", nil, 0)},
	}
	work := []beads.Bead{
		workBead("w1", "claude", "dead-session", "in_progress", 5),
	}
	sessions := []beads.Bead{sessionBead("dead-session", "closed")}

	result := ComputePoolDesiredStates(cfg, work, sessions, nil)

	// The session bead is closed, so this shouldn't be a resume request.
	// It also shouldn't be a new request because it has an assignee.
	total := 0
	for _, ds := range result {
		total += len(ds.Requests)
	}
	if total != 0 {
		t.Errorf("total = %d, want 0 (closed session, assigned bead — orphaned)", total)
	}
}

func TestComputePoolDesiredStates_DedupsResumeForSameSession(t *testing.T) {
	cfg := &config.City{
		Agents: []config.Agent{poolAgent("claude", "", nil, 0)},
	}
	// Two beads assigned to the same session.
	work := []beads.Bead{
		workBead("w1", "claude", "sess-1", "in_progress", 5),
		workBead("w2", "claude", "sess-1", "open", 3),
	}
	sessions := []beads.Bead{sessionBead("sess-1", "open")}

	result := ComputePoolDesiredStates(cfg, work, sessions, nil)

	// Should deduplicate — only one resume request for sess-1.
	resumeCount := 0
	for _, ds := range result {
		for _, req := range ds.Requests {
			if req.Tier == "resume" {
				resumeCount++
			}
		}
	}
	if resumeCount != 1 {
		t.Errorf("resume count = %d, want 1 (deduped)", resumeCount)
	}
}

func TestComputePoolDesiredStates_PriorityOrder(t *testing.T) {
	cfg := &config.City{
		Agents: []config.Agent{poolAgent("claude", "", intPtr(2), 0)},
	}
	work := []beads.Bead{
		workBead("w-low", "claude", "", "open", 1),
		workBead("w-high", "claude", "", "open", 10),
		workBead("w-mid", "claude", "", "open", 5),
	}

	result := ComputePoolDesiredStates(cfg, work, nil, nil)

	if len(result) != 1 || len(result[0].Requests) != 2 {
		t.Fatalf("expected 2 requests, got %d", len(result[0].Requests))
	}
	// Highest priority beads should be accepted.
	if result[0].Requests[0].BeadPriority != 10 {
		t.Errorf("first priority = %d, want 10", result[0].Requests[0].BeadPriority)
	}
	if result[0].Requests[1].BeadPriority != 5 {
		t.Errorf("second priority = %d, want 5", result[0].Requests[1].BeadPriority)
	}
}

func TestComputePoolDesiredStates_SuspendedAgentSkipped(t *testing.T) {
	cfg := &config.City{
		Agents: []config.Agent{
			{Name: "claude", Suspended: true, Pool: &config.PoolConfig{Max: -1}},
		},
	}
	work := []beads.Bead{
		workBead("w1", "claude", "", "open", 5),
	}

	result := ComputePoolDesiredStates(cfg, work, nil, nil)

	total := 0
	for _, ds := range result {
		total += len(ds.Requests)
	}
	if total != 0 {
		t.Errorf("total = %d, want 0 (agent suspended)", total)
	}
}
