package diagnostics

import (
	"encoding/json"
	"net"
	"strconv"
	"time"
)

func parseIPAPI(body, address string, at time.Time) IPResult {
	r := IPResult{Family: "provider_flags", Source: "https://ipapi.is/developers.html", Address: address, CheckedAt: at}
	var data struct {
		IP         string          `json:"ip"`
		Datacenter *bool           `json:"is_datacenter"`
		VPN        *bool           `json:"is_vpn"`
		Proxy      *bool           `json:"is_proxy"`
		Tor        *bool           `json:"is_tor"`
		Abuser     *bool           `json:"is_abuser"`
		Bogon      *bool           `json:"is_bogon"`
		ASN        json.RawMessage `json:"asn"`
	}
	if len(body) > 8192 || json.Unmarshal([]byte(body), &data) != nil || net.ParseIP(data.IP) == nil || net.ParseIP(data.IP).String() != address {
		r.Status = Status{"failed", "invalid ipapi.is response or mismatched IP"}
		return r
	}
	r.ProviderFlags = map[string]bool{}
	for name, v := range map[string]*bool{"is_datacenter": data.Datacenter, "is_vpn": data.VPN, "is_proxy": data.Proxy, "is_tor": data.Tor, "is_abuser": data.Abuser, "is_bogon": data.Bogon} {
		if v != nil {
			r.ProviderFlags[name] = *v
		}
	}
	var asn struct {
		ASN int64  `json:"asn"`
		Org string `json:"org"`
	}
	if json.Unmarshal(data.ASN, &asn) == nil && asn.ASN > 0 && len(asn.Org) <= 256 {
		r.ASN = "AS" + strconv.FormatInt(asn.ASN, 10)
		r.Organization = asn.Org
	}
	if data.Datacenter == nil && data.VPN == nil && data.Proxy == nil && data.Tor == nil && data.Abuser == nil {
		r.Status = Status{"partial", "provider returned no datacenter/VPN/proxy/Tor/abuser flags"}
	} else {
		r.Status = Status{Status: "ok", Reason: "ipapi.is supplied flags; missing fields remain unknown"}
	}
	return r
}
