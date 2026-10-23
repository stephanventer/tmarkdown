package main

import (
	"strings"
	"testing"
)

func TestHelpViewFits(t *testing.T) {
	m := newModel("", "x", []byte("# x"), testView(false))
	m.w, m.h = 100, 40
	out := m.helpView()
	if n := len(strings.Split(out, "\n")); n != 39 {
		t.Fatalf("help is %d rows, want 39", n)
	}
	plain := string(stripANSI(out))
	for _, want := range []string{"Scrolling", "Finding things", "General", "Shift+Tab", "Ctrl+c"} {
		if !strings.Contains(plain, want) {
			t.Errorf("help missing %q", want)
		}
	}
	t.Log("\n" + plain)
}
