package dns

import (
	"context"
	"log"
	"net"
	"strings"

	"mailshield/internal/core"
)

// Verdicter implements core.Verdicter using real DNS lookups.
// Etap 1: SPF check only. DKIM verify comes in Etap 1.2 (roadmap).
type Verdicter struct{}

func New() *Verdicter { return &Verdicter{} }

func (v *Verdicter) Analyze(_ context.Context, e core.ParsedEmail) core.Verdict {
	spf := checkSPF(e.From, e.SenderIP)

	risk := 1
	label := "clean"
	if spf == "fail" {
		risk = 6
		label = "suspicious"
	}

	log.Printf("[dns/verdicter] from=%s ip=%s spf=%s risk=%d", e.From, e.SenderIP, spf, risk)
	return core.Verdict{
		SPF:   spf,
		DKIM:  "none", // real DKIM verify: roadmap Etap 1.2
		Risk:  risk,
		Label: label,
	}
}

// checkSPF does a basic DNS TXT lookup to validate the sender's IP against SPF records.
func checkSPF(fromEmail, senderIP string) string {
	parts := strings.Split(fromEmail, "@")
	if len(parts) < 2 {
		return "none"
	}
	domain := parts[1]

	records, err := net.LookupTXT(domain)
	if err != nil {
		log.Printf("[dns/verdicter] SPF lookup failed for %s: %v", domain, err)
		return "none"
	}

	for _, r := range records {
		if !strings.HasPrefix(r, "v=spf1") {
			continue
		}
		// IP directly mentioned → pass
		if senderIP != "" && strings.Contains(r, senderIP) {
			return "pass"
		}
		// permissive qualifiers → pass
		if strings.Contains(r, "+all") || strings.Contains(r, "redirect=") {
			return "pass"
		}
		// strict reject → fail
		if strings.Contains(r, "-all") {
			return "fail"
		}
		// soft fail
		if strings.Contains(r, "~all") {
			return "softfail"
		}
		return "none"
	}
	return "none"
}
