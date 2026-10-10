package model

import "testing"

func TestPurposeCapabilityConstraints(t *testing.T) {
	for _, tc := range []struct {
		targets []string
		region  string
		valid   bool
	}{
		{nil, "", true}, {[]string{"egress", "youtube", "netflix"}, "US", true}, {[]string{"netflix"}, "JP", true}, {[]string{"youtube"}, "", true}, {nil, "US", false}, {[]string{"youtube"}, "US", false}, {[]string{"egress", "egress"}, "", false}, {[]string{"egress", "youtube", "netflix", "egress"}, "", false},
	} {
		p := Purpose{Slug: "daily", Name: "Daily", Kind: "daily", RequiredCapabilities: tc.targets, CapabilityRegion: tc.region}
		p.Normalize()
		err := p.Validate()
		if (err == nil) != tc.valid {
			t.Fatalf("%+v: %v", tc, err)
		}
	}
}

func TestPurposeMetadataAndValidation(t *testing.T) {
	p := Purpose{Slug: "media", Name: "Media", Kind: "media", Regions: []string{"US"}, Lines: []string{"premium"}, Tags: []string{"media"}}
	p.Normalize()
	if err := p.Validate(); err != nil {
		t.Fatal(err)
	}
	n := PurposeMetadata{Region: "US", Line: "premium", Purposes: []string{"daily", "media"}}
	if !p.Matches(n) {
		t.Fatal("expected match")
	}
	if p.Matches(EffectivePurposeMetadata(n, PurposeMetadata{Region: "JP"})) {
		t.Fatal("host region override ignored")
	}
	if p.Matches(EffectivePurposeMetadata(n, PurposeMetadata{Purposes: []string{"daily"}})) {
		t.Fatal("host tags override ignored")
	}
	for _, mutate := range []func(*Purpose){func(p *Purpose) { p.Slug = "../x" }, func(p *Purpose) { p.DailyPurpose = p.Slug }, func(p *Purpose) { p.RequiredCapabilities = []string{"unknown"} }, func(p *Purpose) { p.CapabilityRegion = "usa" }, func(p *Purpose) { p.Domains = []string{"bad,domain"} }, func(p *Purpose) { p.Interval = 1 }, func(p *Purpose) { p.TestURL = "file:///tmp/test" }} {
		bad := p
		mutate(&bad)
		if err := bad.Validate(); err == nil {
			t.Fatalf("accepted %+v", bad)
		}
	}
}
