package dns

import (
	"context"

	"c2go/config"
)

// Provider defines the interface for updating DNS records across multiple domains.
type Provider interface {
	// UpdateDomains updates the DNS records for the provided domains to point to the new IP.
	UpdateDomains(ctx context.Context, ip string, managedZones []config.ManagedZone) error
}
