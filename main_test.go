package main

import "testing"

func TestNormalizePhone(t *testing.T) {
	cases := []struct {
		input, want string
		valid       bool
	}{
		{"8 (999) 123-45-67", "+79991234567", true},
		{"+7 999 123 45 67", "+79991234567", true},
		{"9991234567", "+79991234567", true},
		{"12345", "", false},
		{"abc9991234567", "", false},
	}
	for _, tc := range cases {
		got, ok := normalizePhone(tc.input)
		if got != tc.want || ok != tc.valid {
			t.Errorf("normalizePhone(%q) = (%q, %v), want (%q, %v)", tc.input, got, ok, tc.want, tc.valid)
		}
	}
}

func TestTransformDeduplicatesByAmountThenDate(t *testing.T) {
	orders := []Order{
		{Name: "Old", Phone: "8 999 123-45-67", Email: "old@example.com", Amount: 100, CreatedAt: "2026-01-01T10:00:00Z"},
		{Name: "Best", Phone: "+7 (999) 123-45-67", Email: "best@example.com", Amount: 500, CreatedAt: "2026-01-01T09:00:00Z"},
		{Name: "Later", Phone: "9991234567", Email: "later@example.com", Amount: 500, CreatedAt: "2026-01-02T09:00:00Z"},
		{Name: "Bad", Phone: "12", Amount: 1000},
	}
	got := transform(orders)
	if len(got.Clients) != 1 {
		t.Fatalf("got %d clients, want 1", len(got.Clients))
	}
	if got.Clients[0].Name != "Later" {
		t.Errorf("winner = %q, want Later", got.Clients[0].Name)
	}
	if got.Stats.Received != 4 || got.Stats.Unique != 1 || got.Stats.DuplicatesMerged != 2 || got.Stats.InvalidDropped != 1 {
		t.Errorf("unexpected stats: %+v", got.Stats)
	}
}
