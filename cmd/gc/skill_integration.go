package main

import (
	"fmt"
	"io"
	"path/filepath"
	"strings"

	"github.com/gastownhall/gascity/internal/config"
	"github.com/gastownhall/gascity/internal/materialize"
	"github.com/gastownhall/gascity/internal/runtime"
	"github.com/gastownhall/gascity/internal/shellquote"
)

// isStage2EligibleSession reports whether skill materialization should
// run for the given agent's session runtime. Per the skill-
// materialization spec (§ "Stage 2 runtime gate") and the runtime
// reality of which providers actually execute PreStart:
//
//	tmux  → eligible. PreStart runs on the host via tmux/adapter.go
//	        runPreStart before the tmux session is created.
//	""    → eligible (workspace default maps to tmux).
//	acp   → ineligible. Session runs in-process; out of scope v0.15.1.
//	k8s   → ineligible. PreStart runs inside the pod; gc binary and
//	        host skill paths aren't available there.
//
// The spec lists subprocess as eligible, but as of v0.15.1 the
// subprocess runtime in internal/runtime/subprocess does NOT execute
// cfg.PreStart — it only stages CopyFiles and overlay content before
// exec'ing the command. Marking subprocess eligible would inject a
// PreStart entry that never runs, silently dropping materialization.
// The conservative fix is to exclude subprocess from eligibility here
// until the subprocess runtime gains PreStart support (tracked as a
// follow-up for Phase 4 / post-v0.15.1).
//
// Hybrid is also ineligible. A default-config hybrid city routes every
// session to local tmux and would work, but once the user configures
// RemoteMatch (or GC_HYBRID_REMOTE_MATCH), some sessions route to
// k8s — and a host-side PreStart would execute on the controller box
// instead of the pod, materializing into the wrong workdir.
// Per-session routing-aware eligibility is Phase 4A work.
//
// Agent.Session == "acp" overrides the city-level session selector at
// the per-agent level — even in a tmux city, an ACP agent is
// ineligible because the session runs in-process.
func isStage2EligibleSession(citySessionProvider string, agent *config.Agent) bool {
	if agent == nil {
		return false
	}
	if agent.Session == "acp" {
		return false
	}
	switch strings.TrimSpace(citySessionProvider) {
	case "", "tmux":
		return true
	default:
		// subprocess, k8s, acp, fake, fail, hybrid, exec:<script>, ...
		// — all conservatively ineligible until individually verified.
		return false
	}
}

// agentScopeRoot returns the canonical absolute filesystem root into
// which stage-1 materialization writes for this agent. City-scoped
// agents resolve to cityPath; rig-scoped agents resolve to the rig's
// configured Path (looked up by agent.Dir). Per spec, empty scope
// defaults to "rig".
//
// The returned path is always absolute and cleaned so callers can
// compare it against an already-resolved workDir without worrying
// about trailing slashes, `./` prefixes, or the user-authored rig path
// being relative to cityPath. This matters because Phase 3B uses
// `workDir != scopeRoot` to decide whether to inject a per-session
// PreStart — a spurious mismatch (e.g., "/city/rig" vs "rig/")
// triggers useless materialization on every spawn.
//
// When the agent is rig-scoped but no matching rig exists in the
// config (e.g., an inline [[agent]] with a bespoke dir), the path
// falls back to cityPath. Callers should treat this as a conservative
// best-effort identifier; a mismatched scope root is used for stage
// discrimination, not as a security boundary.
func agentScopeRoot(agent *config.Agent, cityPath string, rigs []config.Rig) string {
	root := resolveAgentScopeRoot(agent, cityPath, rigs)
	return canonicaliseFilePath(root, cityPath)
}

func resolveAgentScopeRoot(agent *config.Agent, cityPath string, rigs []config.Rig) string {
	if agent == nil {
		return cityPath
	}
	scope := agent.Scope
	if scope == "" {
		scope = "rig"
	}
	if scope == "city" {
		return cityPath
	}
	for _, r := range rigs {
		if r.Name == agent.Dir && r.Path != "" {
			return r.Path
		}
	}
	return cityPath
}

// canonicaliseFilePath returns filepath.Clean(abs(path)), joining
// relative paths against base before cleaning. Falls back to Clean(path)
// when absolute resolution fails. Used to make scope-root and workDir
// strings directly comparable.
func canonicaliseFilePath(path, base string) string {
	if path == "" {
		return ""
	}
	if !filepath.IsAbs(path) {
		path = filepath.Join(base, path)
	}
	if abs, err := filepath.Abs(path); err == nil {
		return filepath.Clean(abs)
	}
	return filepath.Clean(path)
}

// effectiveSkillsForAgent returns the post-precedence desired skill set
// for one agent. Returns nil when the agent's provider has no vendor
// sink, when no catalog produced any entries, or when the agent is
// nil.
//
// Agent-catalog load failures are logged to stderr (matching the
// city-catalog pattern in newAgentBuildParams) so a permissions
// glitch on an agent's skills_dir is observable rather than silently
// dropping agent-local skills.
func effectiveSkillsForAgent(city *materialize.CityCatalog, agent *config.Agent, stderr io.Writer) []materialize.SkillEntry {
	if agent == nil {
		return nil
	}
	if _, ok := materialize.VendorSink(agent.Provider); !ok {
		return nil
	}

	var agentCat materialize.AgentCatalog
	if agent.SkillsDir != "" {
		c, err := materialize.LoadAgentCatalog(agent.SkillsDir)
		switch {
		case err != nil:
			if stderr != nil {
				fmt.Fprintf(stderr, "buildDesiredState: LoadAgentCatalog %q for agent %q: %v (agent-local skills will not contribute to fingerprints this tick)\n", //nolint:errcheck // best-effort stderr
					agent.SkillsDir, agent.QualifiedName(), err)
			}
		default:
			agentCat = c
		}
	}

	sharedCatalog := materialize.CityCatalog{}
	if city != nil {
		sharedCatalog = *city
	}
	desired := materialize.EffectiveSet(sharedCatalog, agentCat)
	if len(desired) == 0 {
		return nil
	}
	return desired
}

// mergeSkillFingerprintEntries adds one "skills:<name>" → content-hash
// entry to fpExtra for each desired skill. Hashes use
// runtime.HashPathContent so any byte-level change to a skill's source
// directory triggers a config-fingerprint drift and drains the agent.
//
// Nil-map safe: allocates fpExtra if the caller passed nil. Returns
// the (possibly new) map. The "skills:" prefix partitions the key
// space so entries cannot collide with other fpExtra keys
// (pool.min/pool.max/wake_mode/etc.).
func mergeSkillFingerprintEntries(fpExtra map[string]string, desired []materialize.SkillEntry) map[string]string {
	if len(desired) == 0 {
		return fpExtra
	}
	if fpExtra == nil {
		fpExtra = make(map[string]string, len(desired))
	}
	for _, e := range desired {
		fpExtra["skills:"+e.Name] = runtime.HashPathContent(e.Source)
	}
	return fpExtra
}

// appendMaterializeSkillsPreStart appends a PreStart command that
// invokes `gc internal materialize-skills --agent <name> --workdir
// <path>` for per-session-worktree materialization. The command is
// APPENDED to any existing user-configured PreStart so worktree
// creation and other setup runs first; materialization runs
// immediately before the agent command.
//
// The gc binary path comes from $GC_BIN (populated by the runtime env
// setup) with "gc" as a fallback if the env var isn't available at
// PreStart expansion time. Argument values are shell-quoted.
func appendMaterializeSkillsPreStart(prestart []string, qualifiedName, workDir string) []string {
	cmd := `"${GC_BIN:-gc}" internal materialize-skills --agent ` +
		shellquote.Join([]string{qualifiedName}) + ` --workdir ` + shellquote.Join([]string{workDir})
	return append(prestart, cmd)
}
