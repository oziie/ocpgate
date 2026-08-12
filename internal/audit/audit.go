// Package audit emits structured JSON audit events describing who accessed
// which cluster, when, and from where. Events go to stdout as one JSON
// object per line, which the Fluentd collector already running on engineer
// machines forwards into the ocpgate-audit-* OpenSearch index.
package audit

import (
	"net"
	"os"
)

// Source identifies the machine an ocpgate invocation ran from. It is
// resolved once at logger construction rather than per event, since it
// cannot change over the life of a process.
type Source struct {
	Hostname string
	IP       string
}

// DetectSource resolves the local hostname and primary non-loopback IPv4
// address. Both are best-effort: an engineer laptop with no route to the
// world still produces usable audit records, just with thinner source
// detail, so failures here degrade to empty strings rather than errors.
func DetectSource() Source {
	var src Source

	if host, err := os.Hostname(); err == nil {
		src.Hostname = host
	}
	src.IP = primaryIPv4()

	return src
}

// primaryIPv4 returns the first non-loopback IPv4 address bound to an up
// interface. This inspects local interfaces only — it deliberately opens no
// connection, so it stays silent and fast on air-gapped networks.
func primaryIPv4() string {
	ifaces, err := net.Interfaces()
	if err != nil {
		return ""
	}

	for _, iface := range ifaces {
		if iface.Flags&net.FlagUp == 0 || iface.Flags&net.FlagLoopback != 0 {
			continue
		}
		addrs, err := iface.Addrs()
		if err != nil {
			continue
		}
		for _, addr := range addrs {
			ipNet, ok := addr.(*net.IPNet)
			if !ok {
				continue
			}
			if ip := ipNet.IP.To4(); ip != nil && !ip.IsLoopback() {
				return ip.String()
			}
		}
	}
	return ""
}

// NopLogger discards every event. It backs `audit.enabled: false`, so the
// wiring in cmd/ never needs a nil check around a Logger.
type NopLogger struct{}

// Log discards the event.
func (NopLogger) Log(AuditEvent) {}
