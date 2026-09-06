package main

import (
	"testing"

	"github.com/Gaurav-Gosain/tuios/internal/config"
)

// TestDaemonByDefaultAndItsOverrides pins the one setting here with a
// user-visible consequence. It decides what a bare "tuios" does, and the
// overrides are the way out when the daemon is the thing that is broken: a
// setting in a config file that can only be undone by editing that file is a
// trap when the daemon it turns on is what will not start.
func TestDaemonByDefaultAndItsOverrides(t *testing.T) {
	on := config.DefaultConfig()
	on.Startup.Daemon = true

	t.Run("on by default", func(t *testing.T) {
		if !useDaemonByDefault(config.DefaultConfig()) {
			t.Error("a fresh config ran standalone; the daemon is the default")
		}
	})

	t.Run("off when cleared", func(t *testing.T) {
		off := config.DefaultConfig()
		off.Startup.Daemon = false
		if useDaemonByDefault(off) {
			t.Error("startup.daemon = false did not take effect")
		}
	})

	t.Run("on when set", func(t *testing.T) {
		if !useDaemonByDefault(on) {
			t.Error("startup.daemon = true did not take effect")
		}
	})

	t.Run("the flag overrides it", func(t *testing.T) {
		standaloneMode = true
		t.Cleanup(func() { standaloneMode = false })
		if useDaemonByDefault(on) {
			t.Error("--standalone did not override startup.daemon")
		}
	})

	t.Run("the environment overrides it", func(t *testing.T) {
		t.Setenv("TUIOS_NO_DAEMON", "1")
		if useDaemonByDefault(on) {
			t.Error("TUIOS_NO_DAEMON=1 did not override startup.daemon")
		}
	})

	t.Run("no config is standalone", func(t *testing.T) {
		if useDaemonByDefault(nil) {
			t.Error("a nil config must not send the user through the daemon path")
		}
	})
}
