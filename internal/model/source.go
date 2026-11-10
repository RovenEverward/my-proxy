package model

import (
	"fmt"
	"net/url"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"
)

const DefaultSourceInterval = 6 * 60 * 60

// SubscriptionSource owns upstream configuration, never managed server state.
// Secret fields are persisted but are never serialized through management APIs.
type SubscriptionSource struct {
	ID              int64      `json:"id"`
	Name            string     `json:"name"`
	URL             string     `json:"-"`
	Enabled         bool       `json:"enabled"`
	IntervalSeconds int64      `json:"interval_seconds"`
	Region          string     `json:"region,omitempty"`
	Revision        int64      `json:"revision"`
	LastAttemptAt   *time.Time `json:"last_attempt_at,omitempty"`
	LastGoodAt      *time.Time `json:"last_good_at,omitempty"`
	LastError       string     `json:"last_error,omitempty"`
	Snapshot        []byte     `json:"-"`
	SnapshotURL     string     `json:"-"`
	Report          []byte     `json:"-"`
}

func (s *SubscriptionSource) Normalize() {
	s.Name = strings.TrimSpace(s.Name)
	s.URL = strings.TrimSpace(s.URL)
	s.Region = strings.ToUpper(strings.TrimSpace(s.Region))
}

func (s SubscriptionSource) Validate() error {
	if s.Name == "" || utf8.RuneCountInString(s.Name) > 64 || strings.ContainsAny(s.Name, "\r\n") {
		return fmt.Errorf("来源名称须为 1–64 个字，不能包含换行")
	}
	u, err := url.Parse(s.URL)
	if err != nil || u.Hostname() == "" || (u.Scheme != "http" && u.Scheme != "https") || u.Fragment != "" {
		return fmt.Errorf("订阅地址须为有效的 HTTP 或 HTTPS 地址，不能包含片段")
	}
	if s.IntervalSeconds < 0 || s.IntervalSeconds > 365*24*60*60 {
		return fmt.Errorf("刷新间隔须为 0 到 365 天；0 表示仅手动刷新")
	}
	if s.Region != "" && !ruleCountry.MatchString(s.Region) {
		return fmt.Errorf("地区须为两位国家码，如 JP；也可留空")
	}
	return nil
}

func SourceOrigin(id int64) string { return "source:" + strconv.FormatInt(id, 10) }

func ParseSourceOrigin(value string) (int64, bool) {
	if !strings.HasPrefix(value, "source:") {
		return 0, false
	}
	n, err := strconv.ParseInt(strings.TrimPrefix(value, "source:"), 10, 64)
	return n, err == nil && n > 0
}

func ValidateSourceOrigins(values []string) error {
	for _, v := range values {
		if v == "managed" {
			continue
		}
		if _, ok := ParseSourceOrigin(v); !ok {
			return fmt.Errorf("来源条件只能是 managed 或 source:<来源 ID>")
		}
	}
	return nil
}

func OriginAllowed(values []string, sourceID int64) bool {
	if len(values) == 0 {
		return true
	}
	origin := "managed"
	if sourceID > 0 {
		origin = SourceOrigin(sourceID)
	}
	for _, v := range values {
		if v == origin {
			return true
		}
	}
	return false
}
