package scoring

import "testing"

func fullSignals() Signals {
	return Signals{
		Bool: map[string]bool{
			"has_website": true, "has_email": true, "email_verified": true,
			"has_phone": true, "has_whatsapp": true,
			"industry_match": true, "location_match": true, "employee_match": true,
		},
		Text: map[string]string{},
	}
}

func TestDefaultWeights(t *testing.T) {
	score, breakdown := Evaluate(fullSignals(), nil)
	// 10+15+20+10+10+20+10+15 = 110 -> clamped to 100
	if score != 100 {
		t.Fatalf("score = %d, want 100", score)
	}
	if len(breakdown) != 8 {
		t.Fatalf("breakdown len = %d, want 8", len(breakdown))
	}
}

func TestEmptySignals(t *testing.T) {
	score, _ := Evaluate(Signals{Bool: map[string]bool{}, Text: map[string]string{}}, nil)
	if score != 0 {
		t.Fatalf("score = %d, want 0", score)
	}
	if got := Label(0); got != "Low" {
		t.Fatalf("label = %q", got)
	}
}

func TestCustomRules(t *testing.T) {
	sig := Signals{
		Bool: map[string]bool{},
		Text: map[string]string{"city": "Jakarta", "industry": "Construction"},
	}
	custom := []Rule{
		{Name: "Jakarta bonus", Signal: "city", Operator: "equals", Value: "jakarta", Weight: 25},
		{Name: "Construction", Signal: "industry", Operator: "contains", Value: "struct", Weight: 10},
		{Name: "No match", Signal: "city", Operator: "equals", Value: "bandung", Weight: 50},
	}
	score, breakdown := Evaluate(sig, custom)
	if score != 35 {
		t.Fatalf("score = %d, want 35", score)
	}
	if len(breakdown) != 2 {
		t.Fatalf("breakdown len = %d, want 2", len(breakdown))
	}
}

func TestStatusMapping(t *testing.T) {
	if StatusForScore(95) != "hot" || StatusForScore(70) != "qualified" || StatusForScore(10) != "new" {
		t.Fatal("status mapping wrong")
	}
	if Label(90) != "Hot" || Label(70) != "Strong" || Label(50) != "Medium" || Label(49) != "Low" {
		t.Fatal("label mapping wrong")
	}
}
