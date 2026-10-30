package main

import (
	"context"
	"io"
	"log/slog"
	"net"
	"strings"
	"testing"
	"time"
)

func quietLogger() *slog.Logger { return slog.New(slog.NewTextHandler(io.Discard, nil)) }

func setBootEnv(t *testing.T) {
	t.Helper()
	t.Setenv("DATABASE_URL", "postgres://u@127.0.0.1:1/db?sslmode=disable")
	t.Setenv("A2A_AGENT_URL", "http://product-master-a2a.warehouse-systems.svc.cluster.local:8093/")
}

func TestLoadBootConfigAcceptsACompleteEnvironment(t *testing.T) {
	setBootEnv(t)
	cfg, err := loadBootConfig()
	if err != nil || cfg.databaseURL == "" || cfg.agentURL == "" {
		t.Fatalf("config = %+v, %v", cfg, err)
	}
	t.Setenv("A2A_AGENT_URL", "  https://agent.example.test/a2a  ")
	if cfg, err = loadBootConfig(); err != nil || cfg.agentURL != "https://agent.example.test/a2a" {
		t.Fatalf("a padded https URL must be accepted and trimmed, got %+v, %v", cfg, err)
	}
}

// Each missing or rejected value is a boot error that names its variable.
func TestLoadBootConfigRefusesEachMissingOrRejectedValue(t *testing.T) {
	cases := []struct {
		name, variable, value, want string
	}{
		{"database unset", "DATABASE_URL", "", "DATABASE_URL is required: this context runs on real Postgres"},
		{"agent url unset", "A2A_AGENT_URL", "", "A2A_AGENT_URL is required: the Agent Card must advertise"},
		{"agent url blank", "A2A_AGENT_URL", "   ", "A2A_AGENT_URL is required"},
		{"agent url relative", "A2A_AGENT_URL", "/a2a", `A2A_AGENT_URL="/a2a" is not an absolute http(s) URL`},
		{"agent url schemeless", "A2A_AGENT_URL", "product-master-a2a:8093", "is not an absolute http(s) URL"},
		{"agent url wrong scheme", "A2A_AGENT_URL", "ftp://host/", "is not an absolute http(s) URL"},
		{"agent url no host", "A2A_AGENT_URL", "http:///a2a", "is not an absolute http(s) URL"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			setBootEnv(t)
			t.Setenv(tc.variable, tc.value)
			_, err := loadBootConfig()
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("%s=%q: %v, want a boot error containing %q", tc.variable, tc.value, err, tc.want)
			}
			if !strings.Contains(err.Error(), tc.variable) {
				t.Fatalf("the error does not name %s: %v", tc.variable, err)
			}
		})
	}
}

// Every problem is reported at once, each naming its variable.
func TestLoadBootConfigReportsEveryProblemAtOnce(t *testing.T) {
	t.Setenv("DATABASE_URL", "")
	t.Setenv("A2A_AGENT_URL", "")
	_, err := loadBootConfig()
	if err == nil || !strings.Contains(err.Error(), "DATABASE_URL is required") || !strings.Contains(err.Error(), "A2A_AGENT_URL is required") {
		t.Fatalf("loadBootConfig = %v, want both variables named", err)
	}
}

// run fails fast on a bad configuration: before telemetry, any connection and
// before the listener binds.
func TestRunFailsFastBeforeBindingTheListener(t *testing.T) {
	for _, missing := range []string{"DATABASE_URL", "A2A_AGENT_URL"} {
		t.Run(missing, func(t *testing.T) {
			setBootEnv(t)
			t.Setenv(missing, "")
			l, err := (&net.ListenConfig{}).Listen(t.Context(), "tcp", "127.0.0.1:0")
			if err != nil {
				t.Fatal(err)
			}
			addr := l.Addr().String()
			_ = l.Close()
			t.Setenv("A2A_ADDR", addr)

			start := time.Now()
			if err := run(); err == nil || !strings.Contains(err.Error(), missing+" is required") {
				t.Fatalf("run = %v, want a boot error naming %s", err, missing)
			}
			if time.Since(start) > 5*time.Second {
				t.Fatal("a configuration error must not wait on any dependency")
			}
			if c, derr := (&net.Dialer{Timeout: 200 * time.Millisecond}).DialContext(t.Context(), "tcp", addr); derr == nil {
				_ = c.Close()
				t.Fatal("the listener was bound although the configuration is invalid")
			}
		})
	}
}

// An unreachable database is a boot error after the bounded retries, never a
// silent fallback to another store.
func TestBuildDeps_BadDatabaseFailsBoot(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel() // a cancelled context makes bootretry give up at once
	if _, _, err := buildDeps(ctx, quietLogger(), "postgres://u@127.0.0.1:1/db", "postgres://u@127.0.0.1:1/db"); err == nil {
		t.Fatal("an unreachable database must fail boot")
	}
}

// DATABASE_URL is mandatory: there is no in-memory repository to fall back
// to, and the error names the variable.
func TestBuildDeps_RefusesAnEmptyDatabaseURL(t *testing.T) {
	_, closeFn, err := buildDeps(context.Background(), quietLogger(), "", "")
	if err == nil || !strings.Contains(err.Error(), "DATABASE_URL is required") {
		t.Fatalf("buildDeps with no DATABASE_URL = %v, want a boot error naming it", err)
	}
	closeFn()
}

func TestGetenv(t *testing.T) {
	t.Setenv("PM_A2A_TEST_SET", "v")
	t.Setenv("PM_A2A_TEST_EMPTY", "")
	if got := getenv("PM_A2A_TEST_SET", "d"); got != "v" {
		t.Errorf("set: %q", got)
	}
	if got := getenv("PM_A2A_TEST_EMPTY", "d"); got != "d" {
		t.Errorf("empty: %q", got)
	}
}

func TestNewLoggerLevels(t *testing.T) {
	for level, want := range map[string]slog.Level{"debug": slog.LevelDebug, "WARN": slog.LevelWarn, "warning": slog.LevelWarn, "error": slog.LevelError, "": slog.LevelInfo, "bogus": slog.LevelInfo} {
		if got := newLogger(level); !got.Handler().Enabled(context.Background(), want) || (want > slog.LevelDebug && got.Handler().Enabled(context.Background(), want-1)) {
			t.Errorf("newLogger(%q): level %v not the minimum enabled", level, want)
		}
	}
}
