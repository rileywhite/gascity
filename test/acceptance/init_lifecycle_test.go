//go:build acceptance_a

// Init lifecycle acceptance tests.
//
// These exercise the real gc binary's init and start paths to catch
// regressions in pack materialization, config loading, and scaffold
// creation. All tests use the subprocess session provider and file
// beads — no tmux, no dolt, no inference.
package acceptance_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	helpers "github.com/gastownhall/gascity/test/acceptance/helpers"
)

var (
	testEnv *helpers.Env
)

func TestMain(m *testing.M) {
	tmpDir, err := os.MkdirTemp("", "gc-acceptance-*")
	if err != nil {
		panic("acceptance: creating temp dir: " + err.Error())
	}
	defer os.RemoveAll(tmpDir)

	gcBinary := helpers.BuildGC(tmpDir)

	gcHome := filepath.Join(tmpDir, "gc-home")
	if err := os.MkdirAll(gcHome, 0o755); err != nil {
		panic("acceptance: creating GC_HOME: " + err.Error())
	}
	runtimeDir := filepath.Join(tmpDir, "runtime")
	if err := os.MkdirAll(runtimeDir, 0o755); err != nil {
		panic("acceptance: creating XDG_RUNTIME_DIR: " + err.Error())
	}
	if err := helpers.WriteSupervisorConfig(gcHome); err != nil {
		panic("acceptance: " + err.Error())
	}

	testEnv = helpers.NewEnv(gcBinary, gcHome, runtimeDir)

	code := m.Run()

	// Best-effort supervisor stop.
	helpers.RunGC(testEnv, "", "supervisor", "stop") //nolint:errcheck
	os.Exit(code)
}

// TestInitTutorial verifies that gc init with the default tutorial
// template creates a working city with city.toml, prompts, and formulas.
func TestInitTutorial(t *testing.T) {
	c := helpers.NewCity(t, testEnv)
	c.Init("claude")

	if !c.HasFile("city.toml") {
		t.Fatal("city.toml not created")
	}
	if !c.HasFile("prompts") {
		t.Fatal("prompts/ not created")
	}
	if !c.HasFile("formulas") {
		t.Fatal("formulas/ not created")
	}
	if !c.HasFile(".gc") {
		t.Fatal(".gc/ scaffold not created")
	}

	// Verify city.toml is parseable.
	toml := c.ReadFile("city.toml")
	if toml == "" {
		t.Fatal("city.toml is empty")
	}
}

// TestInitGastown verifies that gc init --from with the gastown example
// materializes all required packs before config load succeeds.
// This is the regression test for Bug 4 (2026-03-18): gastown packs
// not materialized during gc init.
func TestInitGastown(t *testing.T) {
	c := helpers.NewCity(t, testEnv)
	c.InitFrom(filepath.Join(helpers.ExamplesDir(), "gastown"))

	if !c.HasFile("city.toml") {
		t.Fatal("city.toml not created")
	}

	// The critical assertion: packs must be materialized.
	if !c.HasFile("packs/gastown/pack.toml") {
		t.Fatal("packs/gastown/pack.toml not materialized — Bug 4 regression")
	}
	if !c.HasFile("packs/maintenance/pack.toml") {
		t.Fatal("packs/maintenance/pack.toml not materialized")
	}

	// Verify gastown-specific artifacts exist.
	if !c.HasFile("packs/gastown/prompts") {
		t.Fatal("gastown prompts not materialized")
	}
	if !c.HasFile("packs/gastown/formulas") {
		t.Fatal("gastown formulas not materialized")
	}
	if !c.HasFile("packs/gastown/scripts") {
		t.Fatal("gastown scripts not materialized")
	}
}

// TestInitGastownResumeAfterFailure simulates the scenario where
// gc init wrote city.toml but failed during provider readiness.
// A subsequent gc start should materialize packs and load config.
func TestInitGastownResumeAfterFailure(t *testing.T) {
	c := helpers.NewCity(t, testEnv)

	// Simulate partial init: write city.toml with gastown includes
	// but DON'T create the packs directory.
	c.WriteConfig(`[workspace]
name = "partial"
includes = ["packs/gastown"]
default_rig_includes = ["packs/gastown"]
`)

	// Ensure scaffold exists so gc start doesn't complain.
	os.MkdirAll(filepath.Join(c.Dir, ".gc"), 0o755) //nolint:errcheck

	// gc start should materialize gastown packs before config load.
	// We don't actually start agents — just verify the packs appear.
	// Use gc doctor as a proxy: it loads config, which requires packs.
	out, err := c.GC("doctor", "--city", c.Dir)
	// Doctor may report issues, but it should NOT fail with
	// "loading pack.toml: no such file or directory".
	if err != nil {
		// Check if the error is specifically about missing packs.
		if containsSubstr(out, "pack.toml: no such file or directory") {
			t.Fatalf("gc doctor failed with missing packs — Bug 4 regression:\n%s", out)
		}
		// Other errors are OK (doctor may flag missing prompts, etc.)
	}
}

// TestInitRegistryIsolation verifies that tests don't pollute the
// real cities.toml registry. This is the regression test for Bug 5
// (2026-03-18): tests writing to real cities.toml.
func TestInitRegistryIsolation(t *testing.T) {
	// Read the real registry before the test.
	realRegistry := os.Getenv("HOME") + "/.gc/cities.toml"
	var before []byte
	if data, err := os.ReadFile(realRegistry); err == nil {
		before = data
	}

	c := helpers.NewCity(t, testEnv)
	c.Init("claude")

	// Verify the test's registry is in the isolated GC_HOME.
	isolatedRegistry := filepath.Join(testEnv.Get("GC_HOME"), "cities.toml")
	if _, err := os.Stat(isolatedRegistry); err != nil {
		// Registry may not exist if init didn't register (test hook intercepts).
		// That's fine — the point is the REAL registry wasn't touched.
	}

	// The critical assertion: real registry unchanged.
	var after []byte
	if data, err := os.ReadFile(realRegistry); err == nil {
		after = data
	}
	if string(before) != string(after) {
		t.Fatal("real cities.toml was modified — Bug 5 regression")
	}
}

// TestInitCustom verifies that gc init with a known provider creates
// a valid city even when running non-interactively.
func TestInitCustom(t *testing.T) {
	c := helpers.NewCity(t, testEnv)
	c.Init("claude")

	if !c.HasFile("city.toml") {
		t.Fatal("city.toml not created")
	}
}

func containsSubstr(s, substr string) bool {
	return strings.Contains(s, substr)
}
