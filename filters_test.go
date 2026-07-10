package main

import "testing"

func TestFilterMatchesUIDAncestorContextAndRegex(t *testing.T) {
	event := Event{
		Kind:        EventExec,
		PID:         20,
		ParentPID:   10,
		Command:     "worker --job compile-42",
		Exe:         "/usr/bin/worker",
		User:        "jer",
		UID:         "1000",
		SystemdUnit: "builder.service",
		ContainerID: "abcdef",
		ParentChain: []ProcessSummary{{PID: 10}, {PID: 1}},
	}
	filter := Filter{
		User:              "1000",
		AncestorPID:       1,
		ContainerContains: "cde",
		UnitContains:      "builder",
		IncludeRegex:      []string{`compile-[0-9]+`},
		ExcludeRegex:      []string{`browser`},
		EventMode:         EventModeAll,
	}
	if !filter.Accepts(event) {
		t.Fatalf("expected UID, ancestor, context, and regular expression filters to accept event")
	}
	filter.ExcludeRegex = []string{`compile-[0-9]+`}
	if filter.Accepts(event) {
		t.Fatalf("expected exclusion regular expression to reject event")
	}
}

func TestAllEventModeIncludesCoverageGaps(t *testing.T) {
	if !(Filter{EventMode: EventModeAll}).Accepts(Event{Kind: EventGap}) {
		t.Fatalf("expected all event mode to include capture gaps")
	}
}

func TestRegexFiltersMatchOriginalCase(t *testing.T) {
	event := Event{Kind: EventExec, Command: "Worker Compile-ABC"}
	filter := Filter{EventMode: EventModeAll, IncludeRegex: []string{`Compile-[A-Z]+`}}
	if !filter.Accepts(event) {
		t.Fatalf("expected case-sensitive expression to match original event text")
	}
	filter.IncludeRegex = []string{`compile-[A-Z]+`}
	if filter.Accepts(event) {
		t.Fatalf("expected case-sensitive expression with different case not to match")
	}
}
