package fusion

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
)

func TestBoundedDataPreservesOfficialNumericPrecision(t *testing.T) {
	const number = "9007199254740993"
	for _, padding := range []string{"", strings.Repeat("registry", 25000)} {
		body := []byte(`{"schema_version":"goecs.report/v1","ecs_version":"v0.2.12","components":[{"name":"unlocktests.media","schema_version":"goecs.unlocktests/media-v1","payload":{"results":[{"bytes":` + number + `,"duration":0.1234567890123456789}],"registry":{"matched":"` + padding + `"}}}]}`)
		got, _ := boundedData(body)
		if !json.Valid(got) || !bytes.Contains(got, []byte(number)) || !bytes.Contains(got, []byte("0.1234567890123456789")) {
			t.Fatalf("official numeric measurement changed: %s", got)
		}
	}
}
