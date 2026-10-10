package render

import (
	"github.com/RovenEverward/my-proxy/internal/model"
	"strings"
)

// CapabilityExposureStable rejects username-expanded entrances: one user's test
// cannot certify another user's different address, HTTP Host, or TLS SNI.
func CapabilityExposureStable(n model.Node, in model.Inbound, h model.Host) bool {
	ex := ResolveExposure(n, in, h)
	for _, value := range []string{ex.Address, ex.Host, ex.SNI} {
		if strings.Contains(value, "*") || strings.Contains(value, "{USERNAME}") {
			return false
		}
	}
	return true
}
