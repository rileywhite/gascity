package main

import (
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/gastownhall/gascity/internal/config"
	"github.com/gastownhall/gascity/internal/fsys"
	"github.com/gastownhall/gascity/internal/materialize"
)

// TestResolveTemplateSkillsIntegration is the end-to-end regression for
// Phase 3 pass-1 Claude finding #3. It exercises step 11b of
// resolveTemplate end-to-end and asserts that:
//
//  1. Stage-2 eligible agent (tmux session, non-ACP) with
//     WorkDir == scope root → FPExtra contains skills:<name>; no
//     materialize-skills PreStart entry.
//  2. Stage-2 eligible agent with WorkDir != scope root →
//     FPExtra contains skills:<name>; PreStart ends with the
//     materialize-skills command.
//  3. ACP agent → FPExtra has no skills:*; no PreStart materialize-skills.
//  4. K8s session → FPExtra has no skills:*; no PreStart materialize-skills.
//
// Without this test, a refactor could drop or invert step 11b and the
// helper-level tests would still pass.
func TestResolveTemplateSkillsIntegration(t *testing.T) {
	cityPath := t.TempDir()
	// Minimal city.toml + pack.toml so PackSkillsDir populates and
	// the shared catalog discovery picks up skills/.
	writeTemplateResolveCityConfig(t, cityPath, "file")
	if err := os.WriteFile(filepath.Join(cityPath, "pack.toml"),
		[]byte("[pack]\nname = \"skills-test\"\nversion = \"0.1.0\"\nschema = 2\n"),
		0o644); err != nil {
		t.Fatal(err)
	}
	// Write a skill source that the materializer will enumerate.
	skillDir := filepath.Join(cityPath, "skills", "plan")
	if err := os.MkdirAll(skillDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(skillDir, "SKILL.md"),
		[]byte("---\nname: plan\ndescription: test\n---\nbody\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	// Pre-load the city catalog by calling the real discovery path
	// against cityPath/skills.
	sharedCat, err := materialize.LoadCityCatalog(filepath.Join(cityPath, "skills"))
	if err != nil {
		t.Fatal(err)
	}
	if len(sharedCat.Entries) != 1 || sharedCat.Entries[0].Name != "plan" {
		t.Fatalf("unexpected catalog: %+v", sharedCat)
	}

	makeParams := func(sessionProvider string) *agentBuildParams {
		return &agentBuildParams{
			cityName:  "city",
			cityPath:  cityPath,
			workspace: &config.Workspace{Provider: "claude"},
			providers: map[string]config.ProviderSpec{
				"claude": {Command: "echo", PromptMode: "none", SupportsACP: true},
			},
			lookPath:        func(string) (string, error) { return "/bin/echo", nil },
			fs:              fsys.OSFS{},
			rigs:            []config.Rig{},
			beaconTime:      time.Unix(0, 0),
			beadNames:       make(map[string]string),
			stderr:          io.Discard,
			skillCatalog:    &sharedCat,
			sessionProvider: sessionProvider,
		}
	}

	cases := []struct {
		name              string
		sessionProvider   string
		agent             *config.Agent
		wantSkillsKey     bool // expect FPExtra["skills:plan"] populated
		wantMaterializeCmd bool // expect PreStart ends with materialize-skills invocation
	}{
		{
			name:              "tmux + workdir == scope root",
			sessionProvider:   "tmux",
			agent:             &config.Agent{Name: "mayor", Scope: "city", Provider: "claude"},
			wantSkillsKey:     true,
			wantMaterializeCmd: false,
		},
		{
			name:            "tmux + workdir != scope root",
			sessionProvider: "tmux",
			agent: &config.Agent{
				Name:     "polecat",
				Scope:    "city",
				Provider: "claude",
				WorkDir:  ".gc/worktrees/polecat-1",
			},
			wantSkillsKey:     true,
			wantMaterializeCmd: true,
		},
		{
			name:            "acp session ineligible",
			sessionProvider: "tmux",
			agent: &config.Agent{
				Name:     "witness",
				Scope:    "city",
				Provider: "claude",
				Session:  "acp",
			},
			wantSkillsKey:     false,
			wantMaterializeCmd: false,
		},
		{
			name:              "k8s city session ineligible",
			sessionProvider:   "k8s",
			agent:             &config.Agent{Name: "pod-worker", Scope: "city", Provider: "claude"},
			wantSkillsKey:     false,
			wantMaterializeCmd: false,
		},
	}

	for _, c := range cases {
		c := c
		t.Run(c.name, func(t *testing.T) {
			params := makeParams(c.sessionProvider)
			tp, err := resolveTemplate(params, c.agent, c.agent.QualifiedName(), nil)
			if err != nil {
				t.Fatalf("resolveTemplate: %v", err)
			}

			_, haveKey := tp.FPExtra["skills:plan"]
			if haveKey != c.wantSkillsKey {
				t.Errorf("FPExtra[skills:plan] present = %v, want %v; FPExtra=%+v",
					haveKey, c.wantSkillsKey, tp.FPExtra)
			}
			if haveKey {
				if tp.FPExtra["skills:plan"] == "" {
					t.Errorf("FPExtra[skills:plan] empty; want non-empty hash")
				}
			}

			foundCmd := false
			for _, entry := range tp.Hints.PreStart {
				if strings.Contains(entry, "internal materialize-skills") {
					foundCmd = true
					break
				}
			}
			if foundCmd != c.wantMaterializeCmd {
				t.Errorf("PreStart materialize-skills present = %v, want %v; PreStart=%v",
					foundCmd, c.wantMaterializeCmd, tp.Hints.PreStart)
			}
		})
	}
}
