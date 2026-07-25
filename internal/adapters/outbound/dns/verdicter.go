package dns

import (
	"context"
	"log/slog"
	"net"
	"strings"
	"time"

	"mailshield/internal/core"
)

// Verdicter implements core.Verdicter using real DNS lookups.
// Etap 1: SPF check only. DKIM verify comes in Etap 1.2 (roadmap).
type Verdicter struct{}

func New() *Verdicter { return &Verdicter{} }

func (v *Verdicter) Analyze(ctx context.Context, e core.ParsedEmail) core.Verdict {
	spf := checkSPF(ctx, e.From, e.SenderIP)

	risk := 1
	label := "clean"
	if spf == "fail" {
		risk = 6
		label = "suspicious"
	}

	slog.Info("spf check", "from", e.From, "ip", e.SenderIP, "spf", spf, "risk", risk)
	return core.Verdict{
		SPF:   spf,
		DKIM:  "none", // real DKIM verify: roadmap Etap 1.2
		Risk:  risk,
		Label: label,
	}
}

var resolver = &net.Resolver{}

// checkSPF does a basic DNS TXT lookup to validate the sender's IP against SPF records.
func checkSPF(ctx context.Context, fromEmail, senderIP string) string {
	parts := strings.Split(fromEmail, "@")
	if len(parts) < 2 {
		return "none"
	}
	domain := parts[1]

	tctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()

	records, err := resolver.LookupTXT(tctx, domain)
	if err != nil {
		slog.Warn("spf lookup failed", "domain", domain, "err", err)
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
