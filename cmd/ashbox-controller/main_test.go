package main

import (
	"testing"
	"time"

	"ashbox/internal/controller"
)

func TestParseFlagsDefaults(t *testing.T) {
	o, err := parseFlags(nil)
	if err != nil {
		t.Fatal(err)
	}
	if o.mesh != controller.MeshNone || o.baggageKey != "routing-key" || o.degradedAfter != 5*time.Minute {
		t.Fatalf("defaults = %+v", o)
	}
}

func TestParseFlags(t *testing.T) {
	o, err := parseFlags([]string{"--mesh=istio", "--baggage-key=sd-routing-key", "--degraded-after=2m"})
	if err != nil {
		t.Fatal(err)
	}
	if o.mesh != controller.MeshIstio || o.baggageKey != "sd-routing-key" || o.degradedAfter != 2*time.Minute {
		t.Fatalf("options = %+v", o)
	}
}

func TestParseFlagsErrors(t *testing.T) {
	for _, args := range [][]string{
		{"--mesh=consul"},
		{"--baggage-key="},
		{"--recovered-after=0s"},
	} {
		if _, err := parseFlags(args); err == nil {
			t.Errorf("%v: want error", args)
		}
	}
}

func TestNewScheme(t *testing.T) {
	if _, err := newScheme(); err != nil {
		t.Fatal(err)
	}
}
