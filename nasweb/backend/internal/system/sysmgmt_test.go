package system

import "testing"

func TestParseJournalShortISO(t *testing.T) {
	in := "2026-07-10T09:37:58+02:00 ollozunaos nasd[738]: started listening\n" +
		"2026-07-10T09:38:07+02:00 ollozunaos systemd[1]: dev-md0.device: Job failed\n" +
		"-- Boot 123 --\n"
	got := parseJournal(in)
	if len(got) != 2 {
		t.Fatalf("want 2 entries, got %d", len(got))
	}
	if got[0].Time != "2026-07-10T09:37:58+02:00" {
		t.Errorf("time not parsed: %q", got[0].Time)
	}
	if got[0].Unit != "nasd" {
		t.Errorf("unit=%q want nasd", got[0].Unit)
	}
	if got[0].Message != "started listening" {
		t.Errorf("msg=%q", got[0].Message)
	}
	if got[1].Level != "error" {
		t.Errorf("level=%q want error", got[1].Level)
	}
}

func TestValidUnit(t *testing.T) {
	if validUnit("evil;rm") {
		t.Error("evil;rm should be rejected")
	}
	if !validUnit("nasd") {
		t.Error("nasd should be valid")
	}
}
