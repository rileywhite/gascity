package acceptancehelpers

import (
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// Env builds an isolated environment for acceptance tests.
// It filters the host environment to a safe allowlist, then layers
// test-specific overrides on top.
type Env struct {
	vars map[string]string
}

// NewEnv creates an isolated environment with the minimum inherited
// variables (PATH, TMPDIR, locale, shell) plus test-specific overrides
// for GC_HOME and XDG_RUNTIME_DIR.
func NewEnv(gcBinary, gcHome, runtimeDir string) *Env {
	e := &Env{vars: make(map[string]string)}

	// Inherit minimum from host.
	for _, key := range []string{
		"PATH", "TMPDIR", "LANG", "LC_ALL", "USER", "HOME",
		"SHELL", "SSH_AUTH_SOCK", "TERM",
	} {
		if v := os.Getenv(key); v != "" {
			e.vars[key] = v
		}
	}

	// Prepend gc binary dir to PATH.
	if gcBinary != "" {
		e.vars["PATH"] = filepath.Dir(gcBinary) + ":" + e.vars["PATH"]
	}

	// Test isolation.
	e.vars["GC_HOME"] = gcHome
	e.vars["XDG_RUNTIME_DIR"] = runtimeDir
	e.vars["GC_DOLT"] = "skip"
	e.vars["GC_BEADS"] = "file"
	e.vars["GC_SESSION"] = "subprocess"

	return e
}

// With sets a variable, returning the Env for chaining.
func (e *Env) With(key, val string) *Env {
	e.vars[key] = val
	return e
}

// Without removes a variable.
func (e *Env) Without(key string) *Env {
	delete(e.vars, key)
	return e
}

// List returns the environment as a []string for exec.Cmd.Env.
func (e *Env) List() []string {
	out := make([]string, 0, len(e.vars))
	for k, v := range e.vars {
		out = append(out, k+"="+v)
	}
	return out
}

// Get returns a variable's value.
func (e *Env) Get(key string) string {
	return e.vars[key]
}

// WriteSupervisorConfig writes a supervisor.toml with an isolated port.
func WriteSupervisorConfig(gcHome string) error {
	port, err := reservePort()
	if err != nil {
		return fmt.Errorf("reserving supervisor port: %w", err)
	}
	cfg := fmt.Sprintf("[supervisor]\nport = %d\nbind = \"127.0.0.1\"\n", port)
	return os.WriteFile(filepath.Join(gcHome, "supervisor.toml"), []byte(cfg), 0o644)
}

func reservePort() (int, error) {
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return 0, err
	}
	port := l.Addr().(*net.TCPAddr).Port
	l.Close()
	return port, nil
}

// RunGC runs the gc binary with the given args in the given environment.
func RunGC(env *Env, dir string, args ...string) (string, error) {
	gcPath := findInPath(env.Get("PATH"), "gc")
	if gcPath == "" {
		return "", fmt.Errorf("gc not found in PATH")
	}
	cmd := exec.Command(gcPath, args...)
	if dir != "" {
		cmd.Dir = dir
	}
	cmd.Env = env.List()
	out, err := cmd.CombinedOutput()
	return string(out), err
}

func findInPath(pathEnv, name string) string {
	for _, dir := range strings.Split(pathEnv, ":") {
		p := filepath.Join(dir, name)
		if _, err := os.Stat(p); err == nil {
			return p
		}
	}
	return ""
}
