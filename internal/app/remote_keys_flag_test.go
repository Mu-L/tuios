package app

import (
	"testing"

	tea "charm.land/bubbletea/v2"
)

// TestRemoteSendKeysDispatchUnderTheRemoteFlag pins the wiring the input
// handler's remote-key policy depends on: every key of a send-keys sequence is
// dispatched with ProcessingRemoteKeys set, and the flag is down again once
// the sequence is done. The handler reads the flag to leave a scrolled pane
// scrolled (internal/input, remote_key_scroll_test.go); a flag raised late or
// cleared early would let the first or last key through as the person's.
//
// NEGATIVE CONTROL: startRemoteSendKeys not raising the flag fails this on the
// first key; RemoteKeysDoneMsg not clearing it fails it at the end.
func TestRemoteSendKeysDispatchUnderTheRemoteFlag(t *testing.T) {
	m := newNarrowOS(t, 80, 24)
	var seen []bool
	previous := getInputHandler()
	SetInputHandler(func(_ tea.Msg, o *OS) (tea.Model, tea.Cmd) {
		seen = append(seen, o.ProcessingRemoteKeys)
		return o, nil
	})
	t.Cleanup(func() { SetInputHandler(previous) })

	cmd, err := m.startRemoteSendKeys("j k", false, false, "", "req-1")
	if err != nil {
		t.Fatal(err)
	}
	msg := cmd()
	for msg != nil {
		model, next := m.Update(msg)
		m = model.(*OS)
		if next == nil {
			break
		}
		msg = next()
	}
	if len(seen) != 2 || !seen[0] || !seen[1] {
		t.Fatalf("keys dispatched under the remote flag: %v, want [true true]", seen)
	}
	if m.ProcessingRemoteKeys {
		t.Fatal("the remote flag is still up after the sequence finished")
	}
}
