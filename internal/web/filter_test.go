package web

import (
	"strings"
	"testing"

	"leadforge/web/pages/leads"
)

// TestLeadWherePlaceholders guards the $1=tenant convention: every filter
// arg must be $2..$N in order.
func TestLeadWherePlaceholders(t *testing.T) {
	f := leads.Filter{Tab: "qualified", Keyword: "acme", MinScore: 50, City: "Jakarta", SearchID: "11111111-1111-1111-1111-111111111111"}
	w, args := leadWhere(f, false)
	if !strings.HasPrefix(w, "l.tenant_id = $1") {
		t.Fatalf("where = %s", w)
	}
	// expect $2..$6 exactly once each, in order
	want := []string{"$2", "$3", "$4", "$5", "$6"}
	if len(args) != len(want) {
		t.Fatalf("args = %v", args)
	}
	for i, p := range want {
		if !strings.Contains(w, " "+p) && !strings.Contains(w, "="+p) && !strings.Contains(w, "("+p) {
			t.Errorf("missing %s in %s", p, w)
		}
		if i > 0 && strings.Index(w, want[i-1]) > strings.Index(w, p) {
			t.Errorf("out of order: %s before %s in %s", p, want[i-1], w)
		}
	}
	if args[0] != "qualified" || args[1] != "%acme%" || args[2] != "%Jakarta%" || args[3] != 50 {
		t.Errorf("args order wrong: %v", args)
	}
}

func TestSegWherePlaceholders(t *testing.T) {
	f := segFilter{MinScore: 80, City: "Bandung", HasWhatsApp: true}
	w, args := segWhere(f)
	if len(args) != 3 { // min, city x2
		t.Fatalf("args = %v", args)
	}
	for _, p := range []string{"$2", "$3", "$4"} {
		if !strings.Contains(w, p) {
			t.Errorf("missing %s in %s", p, w)
		}
	}
}
