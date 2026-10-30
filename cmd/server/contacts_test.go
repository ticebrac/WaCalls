package main

import "testing"

func TestVCardRoundTrip(t *testing.T) {
	v := buildVCard("Maria Silva", "11 99999-8888")
	if got := phoneFromVCard(v); got != "5511999998888" {
		t.Fatalf("phoneFromVCard = %q, want 5511999998888", got)
	}
	c := contactContent("Maria Silva", v)
	if c.Type != "contact" || c.Text != "Maria Silva" || c.FileName != "5511999998888" {
		t.Fatalf("contactContent = %+v", c)
	}
}
