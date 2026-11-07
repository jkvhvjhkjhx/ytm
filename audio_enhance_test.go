package main

import (
	"strings"
	"testing"
)

func TestMPVAudioArgs(t *testing.T) {
	defer setAudioSettings(true, "none")
	setAudioSettings(false, "none")
	if a := mpvAudioArgs(); a != nil {
		t.Fatalf("expected no args, got %v", a)
	}
	setAudioSettings(true, "bogus")
	a := strings.Join(mpvAudioArgs(), " ")
	if !strings.Contains(a, "dynaudnorm") || strings.Contains(a, "equalizer") {
		t.Fatalf("unexpected args %q", a)
	}
	setAudioSettings(true, "bass")
	if a = strings.Join(mpvAudioArgs(), " "); !strings.Contains(a, "equalizer=f=80") {
		t.Fatalf("missing eq: %q", a)
	}
}
