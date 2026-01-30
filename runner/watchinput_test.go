package runner

import (
	"testing"
)

func TestMapKeyToAction(t *testing.T) {
	tests := []struct {
		key  byte
		want watchAction
	}{
		{'\r', actionRerun},
		{'\n', actionRerun},
		{'a', actionRunAll},
		{'f', actionRunFailed},
		{'q', actionQuit},
		{3, actionQuit}, // Ctrl-C
		{'p', actionFilterProject},
		{'t', actionFilterTest},
		{'T', actionFilterTrait},
		{'h', actionHelp},
		{'?', actionHelp},
		{'x', actionNone},
	}

	for _, tt := range tests {
		got := mapKeyToAction(tt.key)
		if got != tt.want {
			t.Errorf("mapKeyToAction(%d) = %d, want %d", tt.key, got, tt.want)
		}
	}
}
