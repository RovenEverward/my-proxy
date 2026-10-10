// Package fusion adapts the pinned official Go ECS executable without executing source.
package fusion

import (
	"bytes"
	"encoding/json"
	"fmt"
	"regexp"
	"strings"
	"time"
	"unicode/utf8"
)

const (
	Version               = "v0.2.12"
	Source                = "https://github.com/oneclickvirt/ecs/releases/tag/" + Version
	DefaultTimeoutSeconds = 900
	MaxTimeoutSeconds     = 1800
	MaxOutputBytes        = 64 << 10
	MaxDataBytes          = 128 << 10
)

type Config struct {
	Speed          bool `json:"speed"`
	Hardware       bool `json:"hardware"`
	TimeoutSeconds int  `json:"timeout_seconds"`
}

func (c Config) Normalized() (Config, error) {
	if c.TimeoutSeconds == 0 {
		c.TimeoutSeconds = DefaultTimeoutSeconds
	}
	if c.TimeoutSeconds < 60 || c.TimeoutSeconds > MaxTimeoutSeconds {
		return c, fmt.Errorf("fusion timeout must be between 60 and %d seconds", MaxTimeoutSeconds)
	}
	return c, nil
}

type Report struct {
	Engine         string          `json:"engine"`
	Version        string          `json:"version"`
	Source         string          `json:"source"`
	Status         string          `json:"status"`
	StartedAt      time.Time       `json:"started_at"`
	FinishedAt     time.Time       `json:"finished_at"`
	DurationMS     float64         `json:"duration_ms"`
	TimeoutSeconds int             `json:"timeout_seconds"`
	Hardware       bool            `json:"hardware"`
	Speed          bool            `json:"speed"`
	Upload         bool            `json:"upload"`
	Output         string          `json:"output"`
	Data           json.RawMessage `json:"data,omitempty"`
	Truncated      bool            `json:"truncated"`
	Warnings       []string        `json:"warnings,omitempty"`
	Error          string          `json:"error,omitempty"`
}

func NewReport(c Config) Report {
	return Report{Engine: "fusion", Version: Version, Source: Source, Status: "failed", StartedAt: time.Now().UTC(), TimeoutSeconds: c.TimeoutSeconds, Hardware: c.Hardware, Speed: c.Speed, Upload: false}
}

var ansi = regexp.MustCompile(`\x1b(?:\[[0-?]*[ -/]*[@-~]|\][^\x07\x1b]*(?:\x07|\x1b\\)|[@-_])`)

// CleanOutput retains the latest complete UTF-8 evidence within the text budget.
func CleanOutput(s string) (string, bool) {
	s = strings.ToValidUTF8(ansi.ReplaceAllString(s, ""), "�")
	s = strings.Map(func(r rune) rune {
		if r < 32 && r != '\n' && r != '\t' {
			return -1
		}
		if r == 127 {
			return -1
		}
		return r
	}, s)
	truncated := len(s) > MaxOutputBytes
	if truncated {
		s = s[len(s)-MaxOutputBytes:]
		for len(s) > 0 && !utf8.RuneStart(s[0]) {
			s = s[1:]
		}
	}
	return s, truncated
}

// Capture recognizes the upstream versioned report; it does not invent module results.
// Official stdout contains progress text followed by an indented JSON object.
func (r *Report) Capture(out string) {
	const anchor = "{\n  \"schema_version\": \"goecs.report/v1\""
	index := strings.LastIndex(out, anchor)
	if index < 0 {
		r.Output, r.Truncated = CleanOutput(out)
		r.Warnings = append(r.Warnings, "official structured report is absent; retained partial text")
		r.Status = "partial"
		r.Error = "official structured report is absent"
		return
	}
	data := strings.TrimSpace(out[index:])
	var upstream struct {
		Schema   string `json:"schema_version"`
		Version  string `json:"ecs_version"`
		Status   string `json:"status"`
		Text     string `json:"text"`
		Sections []struct {
			Name    string `json:"name"`
			Enabled bool   `json:"enabled"`
			Status  string `json:"status"`
			Reason  string `json:"reason"`
		} `json:"sections"`
		Components []struct {
			Name   string `json:"name"`
			Status string `json:"status"`
			Reason string `json:"reason"`
		} `json:"components"`
	}
	parseErr := json.Unmarshal([]byte(data), &upstream)
	if parseErr == nil && (upstream.Schema != "goecs.report/v1" || upstream.Version != Version) {
		parseErr = fmt.Errorf("unexpected official report schema %q or version %q", upstream.Schema, upstream.Version)
	}
	if parseErr != nil {
		r.Output, r.Truncated = CleanOutput(out)
		r.Status = "partial"
		r.Error = "official structured report: " + parseErr.Error()
		r.Warnings = append(r.Warnings, "official structured report is invalid or has an unexpected version")
		return
	}
	r.Output, r.Truncated = CleanOutput(out[:index] + upstream.Text)
	bounded, dataTruncated := boundedData([]byte(data))
	if dataTruncated {
		r.Truncated = true
		r.Warnings = append(r.Warnings, "official JSON exceeds 128 KiB; retained compact known payloads and component statuses; adapter_omissions and payload_omissions identify omitted evidence")
	}
	r.Data = bounded

	switch upstream.Status {
	case "ok":
		r.Status = "done"
	case "timeout", "canceled":
		r.Status = upstream.Status
	case "error":
		r.Status = "failed"
	default:
		r.Status = "partial"
	}
	for _, section := range upstream.Sections {
		if section.Enabled && section.Status != "ok" {
			r.Warnings = append(r.Warnings, section.Name+": "+section.Status+reasonSuffix(section.Reason))
			if r.Status == "done" {
				r.Status = "partial"
			}
		}
	}
	for _, component := range upstream.Components {
		if component.Status != "ok" && component.Status != "skipped" {
			r.Warnings = append(r.Warnings, component.Name+": "+component.Status+reasonSuffix(component.Reason))
			if r.Status == "done" {
				r.Status = "partial"
			}
		}
	}
	if r.Truncated && r.Status == "done" {
		r.Status = "partial"
	}
	// Reasons are external input too. Bound every field retained alongside raw data.
	if len(r.Warnings) > 32 {
		r.Warnings = r.Warnings[:32]
	}
	for i, w := range r.Warnings {
		if len(w) > 512 {
			w = w[:512]
			w = strings.ToValidUTF8(w, "")
		}
		r.Warnings[i] = w
	}
}

func reasonSuffix(s string) string {
	if s == "" {
		return ""
	}
	return ": " + s
}

func (r *Report) Finish(err error) {
	r.FinishedAt = time.Now().UTC()
	r.DurationMS = float64(r.FinishedAt.Sub(r.StartedAt)) / float64(time.Millisecond)
	if err != nil {
		r.Error, _ = CleanOutput(err.Error())
		if len(r.Error) > 2048 {
			r.Error = r.Error[:2048]
			r.Error = strings.ToValidUTF8(r.Error, "")
		}
	}
}

// boundedData keeps every official component envelope even when its payload
// needs shortening. It never substitutes an omitted measurement with success.
func boundedData(data []byte) (json.RawMessage, bool) {
	// Marshal removes indentation and avoids unnecessarily dropping payloads.
	var root map[string]any
	if !json.Valid(data) {
		return nil, true
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.UseNumber()
	if decoder.Decode(&root) != nil {
		return nil, true
	}
	body, _ := json.Marshal(root)
	if len(body) <= MaxDataBytes {
		return body, false
	}
	// Text already has an independently bounded Output representation.
	if _, exists := root["text"]; exists {
		delete(root, "text")
		root["adapter_omissions"] = []string{"text: retained separately as bounded output"}
	}
	root["adapter_truncated"] = true
	body, _ = json.Marshal(root)
	if len(body) <= MaxDataBytes {
		return body, true
	}
	components, _ := root["components"].([]any)
	if root["schema_version"] == "goecs.report/v1" && root["ecs_version"] == Version {
		for _, entry := range components {
			if item, ok := entry.(map[string]any); ok && compactPayload(item) {
				root["adapter_compacted"] = true
			}
		}
	}
	body, _ = json.Marshal(root)
	if len(body) <= MaxDataBytes {
		return body, true
	}
	body = fitPayloads(root, components)
	if len(body) > MaxDataBytes {
		// Ancillary root fields are not measurements inside a component.
		for _, key := range []string{"tcp", "dns"} {
			if _, exists := root[key]; exists {
				delete(root, key)
				omissions, _ := root["adapter_omissions"].([]string)
				root["adapter_omissions"] = append(omissions, key)
			}
		}
		body, _ = json.Marshal(root)
	}
	if len(body) > MaxDataBytes {
		return json.RawMessage(`{"adapter_truncated":true,"error":"official report envelopes exceed limit"}`), true
	}
	return body, true
}
