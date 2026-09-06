package tape

import "testing"

// run-command is the escape hatch for a binding with no verb, and a binding is
// known by its keymap name. The resolver takes that name, the tape's own name,
// and either in any case, and refuses everything else, so a misspelt command
// is an error rather than a success that ran nothing.
//
// NEGATIVE CONTROLS: commandKey keeping underscores fails the keymap rows;
// ResolveCommandName returning ok for any name fails the last row.
func TestResolveCommandNameTakesTheKeymapName(t *testing.T) {
	rows := []struct {
		name string
		want CommandType
		ok   bool
	}{
		{"ToggleTiling", CommandTypeToggleTiling, true},
		{"toggle_tiling", CommandTypeToggleTiling, true},
		{"toggle_zoom", CommandTypeToggleZoom, true},
		{"TOGGLEZOOM", CommandTypeToggleZoom, true},
		{"switch_workspace", CommandTypeSwitchWS, true},
		{"toggle_zooom", "", false},
		{"", "", false},
	}
	for _, row := range rows {
		got, ok := ResolveCommandName(row.name)
		if ok != row.ok || got != row.want {
			t.Errorf("ResolveCommandName(%q) = %q, %t; want %q, %t", row.name, got, ok, row.want, row.ok)
		}
	}
	if len(commandTypes) != len(commandTypesByKey) {
		t.Fatalf("%d command types fold to %d keys: two commands share a keymap name", len(commandTypes), len(commandTypesByKey))
	}
}
