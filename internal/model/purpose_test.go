package model

import "testing"

func TestPurposeMetadataAndValidation(t *testing.T) {
	n := PurposeMetadata{Region: "US", Line: "premium", Purposes: []string{"daily", "media"}}
	if err := n.Validate(); err != nil {
		t.Fatal(err)
	}
	for _, mutate := range []func(*PurposeMetadata){
		func(m *PurposeMetadata) { m.Region = string(make([]byte, 81)) },
		func(m *PurposeMetadata) { m.Purposes = []string{"../x"} },
		func(m *PurposeMetadata) { m.City = "bad\tcity" },
		func(m *PurposeMetadata) { m.Letter = "lower" },
	} {
		bad := n
		mutate(&bad)
		if err := bad.Validate(); err == nil {
			t.Fatalf("accepted %+v", bad)
		}
	}
}
