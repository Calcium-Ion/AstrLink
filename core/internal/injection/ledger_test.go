package injection

import (
	"fmt"
	"strings"
	"testing"
)

func TestLedgerReplaysByMessage(t *testing.T) {
	ledger := NewLedger()
	first := ledger.Fingerprint("帮我梳理插件接缝")
	if first != ledger.Fingerprint("  帮我梳理插件接缝\r\n") {
		t.Fatal("whitespace at the ends changed the fingerprint")
	}
	if strings.Contains(first, "插件") || first == NewLedger().Fingerprint("帮我梳理插件接缝") {
		t.Fatal("the fingerprint must be a keyed hash of the text")
	}
	ledger.Remember("session_1", first, "<project-memory>\n- a\n</project-memory>")
	if block, found := ledger.Lookup("session_1", first); !found || block != "<project-memory>\n- a\n</project-memory>" {
		t.Fatalf("Lookup = %q, %v", block, found)
	}
	if _, found := ledger.Lookup("session_2", first); found {
		t.Fatal("another session saw the block")
	}
}

func TestLedgerStaysWithinItsLimits(t *testing.T) {
	ledger := NewLedger()
	ledger.maxSessions, ledger.maxBlocks, ledger.maxBytes = 2, 3, 40
	for turn := range 5 {
		ledger.Remember("a", fmt.Sprint(turn), "block")
	}
	for turn, want := range []bool{false, false, true, true, true} {
		if _, found := ledger.Lookup("a", fmt.Sprint(turn)); found != want {
			t.Fatalf("turn %d found = %v, want %v", turn, found, want)
		}
	}
	ledger.Remember("b", "x", "block")
	ledger.Lookup("a", "4") // a is now the most recent
	ledger.Remember("c", "x", "block")
	if _, found := ledger.Lookup("b", "x"); found {
		t.Fatal("the session used longest ago was kept past the session limit")
	}
	ledger.Remember("c", "big", strings.Repeat("x", 30))
	if _, found := ledger.Lookup("a", "4"); found {
		t.Fatal("a session was kept past the byte limit")
	}
	if block, found := ledger.Lookup("c", "big"); !found || len(block) != 30 {
		t.Fatal("the session being written was dropped")
	}
}
