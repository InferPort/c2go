package dns

import (
	"context"
	"fmt"
	"strings"

	"c2go/config"
	"c2go/console"

	"github.com/cloudflare/cloudflare-go"
)

// CloudflareProvider implements the Provider interface for Cloudflare.
type CloudflareProvider struct {
	api *cloudflare.API
}

// NewCloudflareProvider creates a new Cloudflare provider using an API Token.
func NewCloudflareProvider(token string) (*CloudflareProvider, error) {
	api, err := cloudflare.NewWithAPIToken(token)
	if err != nil {
		return nil, fmt.Errorf("failed to initialize cloudflare api: %w", err)
	}

	return &CloudflareProvider{
		api: api,
	}, nil
}

// ListZones queries Cloudflare for all zones available to the token.
func (p *CloudflareProvider) ListZones(ctx context.Context) ([]string, error) {
	zones, err := p.api.ListZones(ctx)
	if err != nil {
		return nil, err
	}

	var names []string
	for _, z := range zones {
		names = append(names, z.Name)
	}
	return names, nil
}

// ListARecords queries Cloudflare for all A and AAAA records in the given zone.
// It removes duplicates and filters only valid A/AAAA records.
func (p *CloudflareProvider) ListARecords(ctx context.Context, domain string) ([]string, error) {
	zoneID, err := p.api.ZoneIDByName(domain)
	if err != nil {
		return nil, err
	}
	rc := cloudflare.ZoneIdentifier(zoneID)

	recs, _, err := p.api.ListDNSRecords(ctx, rc, cloudflare.ListDNSRecordsParams{})
	if err != nil {
		return nil, err
	}

	recordMap := make(map[string]bool)
	var names []string
	for _, r := range recs {
		if r.Type == "A" || r.Type == "AAAA" {
			shortName := r.Name
			if shortName == domain {
				shortName = "@"
			} else if strings.HasSuffix(shortName, "."+domain) {
				shortName = strings.TrimSuffix(shortName, "."+domain)
			}
			
			if !recordMap[shortName] {
				recordMap[shortName] = true
				names = append(names, shortName)
			}
		}
	}
	return names, nil
}

// CreateARecord creates a new A record in the given domain.
func (p *CloudflareProvider) CreateARecord(ctx context.Context, domain, recordName, ip string, proxied bool) error {
	zoneID, err := p.api.ZoneIDByName(domain)
	if err != nil {
		return fmt.Errorf("fallo al obtener Zone ID para %s: %w", domain, err)
	}

	rc := cloudflare.ZoneIdentifier(zoneID)

	fullRecordName := domain
	if recordName != "@" && recordName != domain {
		fullRecordName = fmt.Sprintf("%s.%s", recordName, domain)
	}

	recordType := "A"
	if strings.Contains(ip, ":") {
		recordType = "AAAA"
	}

	// Check if it already exists with an incompatible type
	existing, _, err := p.api.ListDNSRecords(ctx, rc, cloudflare.ListDNSRecordsParams{
		Name: fullRecordName,
	})
	if err != nil {
		return fmt.Errorf("fallo al verificar existencia del registro: %w", err)
	}

	if len(existing) > 0 {
		for _, r := range existing {
			if r.Type != "A" && r.Type != "AAAA" {
				return fmt.Errorf("el registro ya existe con un tipo incompatible (%s)", r.Type)
			}
			if r.Type == recordType && r.Name == fullRecordName {
				// It exists with the same type, we could just update it, but let's error for creation
				return fmt.Errorf("el registro %s tipo %s ya existe", fullRecordName, recordType)
			}
		}
	}

	params := cloudflare.CreateDNSRecordParams{
		Type:    recordType,
		Name:    fullRecordName,
		Content: ip,
		Proxied: &proxied,
		TTL:     1, // 1 = Auto
	}

	_, err = p.api.CreateDNSRecord(ctx, rc, params)
	if err != nil {
		return fmt.Errorf("error de la API de Cloudflare: %w", err)
	}

	return nil
}

// UpdateDomains updates all the specified records for the given domains in Cloudflare.
func (p *CloudflareProvider) UpdateDomains(ctx context.Context, ip string, managedZones []config.ManagedZone) error {
	recordType := "A"
	if strings.Contains(ip, ":") {
		recordType = "AAAA"
	}

	for _, mz := range managedZones {
		zoneID, err := p.api.ZoneIDByName(mz.Domain)
		if err != nil {
			console.LogError("Fallo al obtener Zone ID para %s: %v", mz.Domain, err)
			continue
		}

		rc := cloudflare.ZoneIdentifier(zoneID)

		for _, recordName := range mz.Records {
			fullRecordName := mz.Domain
			if recordName != "@" && recordName != mz.Domain {
				fullRecordName = fmt.Sprintf("%s.%s", recordName, mz.Domain)
			}

			recs, _, err := p.api.ListDNSRecords(ctx, rc, cloudflare.ListDNSRecordsParams{
				Name: fullRecordName,
				Type: recordType,
			})
			if err != nil {
				console.LogError("Fallo al consultar registro %s: %v", fullRecordName, err)
				continue
			}

			if len(recs) == 0 {
				console.LogWait("Registro %s tipo %s no encontrado en %s. Omitiendo.", fullRecordName, recordType, mz.Domain)
				continue
			}

			record := recs[0]
			
			if record.Content == ip {
				console.LogInfo("Registro %s ya está actualizado (%s).", fullRecordName, ip)
				continue
			}

			params := cloudflare.UpdateDNSRecordParams{
				ID:      record.ID,
				Type:    recordType,
				Name:    record.Name,
				Content: ip,
				Proxied: record.Proxied,
				TTL:     record.TTL,
			}

			_, err = p.api.UpdateDNSRecord(ctx, rc, params)
			if err != nil {
				console.LogError("Fallo al actualizar registro %s: %v", fullRecordName, err)
			} else {
				console.LogSuccess("Actualizado %s apuntando a %s", fullRecordName, ip)
			}
		}
	}

	return nil
}
