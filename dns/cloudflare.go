package dns

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"c2go/config"
	"c2go/console"

	"golang.org/x/sync/errgroup"
)

var httpClient = &http.Client{
	Timeout: 30 * time.Second,
}

var apiBaseURL = "https://api.cloudflare.com/client/v4"

type CloudflareProvider struct {
	token string
}

func NewCloudflareProvider(token string) (*CloudflareProvider, error) {
	if token == "" {
		return nil, fmt.Errorf("el API Token no puede estar vacío")
	}

	return &CloudflareProvider{
		token: token,
	}, nil
}

type cfZone struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

type cfZoneListResponse struct {
	Success    bool     `json:"success"`
	Result     []cfZone `json:"result"`
	ResultInfo struct {
		Page       int `json:"page"`
		PerPage    int `json:"per_page"`
		TotalPages int `json:"total_pages"`
		Count      int `json:"count"`
		TotalCount int `json:"total_count"`
	} `json:"result_info"`
}

type cfDNSRecord struct {
	ID      string `json:"id"`
	Type    string `json:"type"`
	Name    string `json:"name"`
	Content string `json:"content"`
	Proxied bool   `json:"proxied"`
	TTL     int    `json:"ttl"`
}

type cfDNSRecordListResponse struct {
	Success    bool           `json:"success"`
	Result     []cfDNSRecord  `json:"result"`
	ResultInfo struct {
		Page       int `json:"page"`
		PerPage    int `json:"per_page"`
		TotalPages int `json:"total_pages"`
		Count      int `json:"count"`
		TotalCount int `json:"total_count"`
	} `json:"result_info"`
}

type cfDNSRecordResponse struct {
	Success bool        `json:"success"`
	Result  cfDNSRecord `json:"result"`
}

func (p *CloudflareProvider) request(ctx context.Context, method, path string, queryParams map[string]string, body interface{}, result interface{}) error {
	apiURL := apiBaseURL + path
	if len(queryParams) > 0 {
		vals := url.Values{}
		for k, v := range queryParams {
			vals.Add(k, v)
		}
		apiURL += "?" + vals.Encode()
	}

	var bodyReader io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return fmt.Errorf("fallo al codificar cuerpo de la petición: %w", err)
		}
		bodyReader = bytes.NewReader(b)
	}

	req, err := http.NewRequestWithContext(ctx, method, apiURL, bodyReader)
	if err != nil {
		return fmt.Errorf("fallo al crear petición http: %w", err)
	}

	req.Header.Set("Authorization", "Bearer "+p.token)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}

	resp, err := httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("petición http fallida: %w", err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("fallo al leer cuerpo de respuesta: %w", err)
	}

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		var cfErr struct {
			Success bool `json:"success"`
			Errors  []struct {
				Code    int    `json:"code"`
				Message string `json:"message"`
			} `json:"errors"`
		}
		if json.Unmarshal(respBody, &cfErr) == nil && len(cfErr.Errors) > 0 {
			var errMsgs []string
			for _, e := range cfErr.Errors {
				errMsgs = append(errMsgs, fmt.Sprintf("%s (código %d)", e.Message, e.Code))
			}
			return fmt.Errorf("error de API Cloudflare (status %d): %s", resp.StatusCode, strings.Join(errMsgs, ", "))
		}
		return fmt.Errorf("API Cloudflare retornó status %d: %s", resp.StatusCode, string(respBody))
	}

	if result != nil {
		if err := json.Unmarshal(respBody, result); err != nil {
			return fmt.Errorf("fallo al decodificar cuerpo de respuesta: %w", err)
		}
	}

	return nil
}

func (p *CloudflareProvider) requestAllPages(ctx context.Context, method, path string, queryParams map[string]string, body interface{}, extract func(json.RawMessage) bool) error {
	baseParams := queryParams
	if baseParams == nil {
		baseParams = make(map[string]string)
	}
	if _, ok := baseParams["per_page"]; !ok {
		baseParams["per_page"] = "100"
	}

	page := 1
	for {
		params := make(map[string]string, len(baseParams)+1)
		for k, v := range baseParams {
			params[k] = v
		}
		params["page"] = fmt.Sprintf("%d", page)

		respWrapper := struct {
			Success    bool            `json:"success"`
			Result     json.RawMessage `json:"result"`
			ResultInfo struct {
				Page       int `json:"page"`
				TotalPages int `json:"total_pages"`
			} `json:"result_info"`
		}{}

		if err := p.request(ctx, method, path, params, body, &respWrapper); err != nil {
			return err
		}

		if !respWrapper.Success {
			return fmt.Errorf("API Cloudflare retornó success=false en página %d", page)
		}

		shouldContinue := extract(respWrapper.Result)
		if !shouldContinue {
			break
		}

		if page >= respWrapper.ResultInfo.TotalPages {
			break
		}
		page++
	}

	return nil
}

func (p *CloudflareProvider) zoneIDByName(ctx context.Context, name string) (string, error) {
	var resp cfZoneListResponse
	err := p.request(ctx, "GET", "/zones", map[string]string{"name": name}, nil, &resp)
	if err != nil {
		return "", err
	}

	if len(resp.Result) == 0 {
		return "", fmt.Errorf("zona no encontrada para el dominio: %s", name)
	}

	return resp.Result[0].ID, nil
}

func (p *CloudflareProvider) ListZones(ctx context.Context) ([]string, error) {
	var names []string

	err := p.requestAllPages(ctx, "GET", "/zones", nil, nil, func(raw json.RawMessage) bool {
		var page cfZoneListResponse
		if err := json.Unmarshal(raw, &page.Result); err != nil {
			return false
		}
		for _, z := range page.Result {
			names = append(names, z.Name)
		}
		return true
	})
	if err != nil {
		return nil, err
	}

	return names, nil
}

func (p *CloudflareProvider) ListARecords(ctx context.Context, domain string) ([]string, error) {
	zoneID, err := p.zoneIDByName(ctx, domain)
	if err != nil {
		return nil, err
	}

	recordMap := make(map[string]bool)
	var names []string

	path := fmt.Sprintf("/zones/%s/dns_records", zoneID)
	err = p.requestAllPages(ctx, "GET", path, nil, nil, func(raw json.RawMessage) bool {
		var page cfDNSRecordListResponse
		if err := json.Unmarshal(raw, &page.Result); err != nil {
			return false
		}
		for _, r := range page.Result {
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
		return true
	})
	if err != nil {
		return nil, err
	}

	return names, nil
}

func (p *CloudflareProvider) CreateARecord(ctx context.Context, domain, recordName, ip string, proxied bool) error {
	zoneID, err := p.zoneIDByName(ctx, domain)
	if err != nil {
		return fmt.Errorf("fallo al obtener Zone ID para %s: %w", domain, err)
	}

	fullRecordName := domain
	if recordName != "@" && recordName != domain {
		fullRecordName = fmt.Sprintf("%s.%s", recordName, domain)
	}

	recordType := "A"
	if strings.Contains(ip, ":") {
		recordType = "AAAA"
	}

	var listResp cfDNSRecordListResponse
	err = p.request(ctx, "GET", fmt.Sprintf("/zones/%s/dns_records", zoneID), map[string]string{"name": fullRecordName}, nil, &listResp)
	if err != nil {
		return fmt.Errorf("fallo al verificar existencia del registro: %w", err)
	}

	if len(listResp.Result) > 0 {
		for _, r := range listResp.Result {
			if r.Type != "A" && r.Type != "AAAA" {
				return fmt.Errorf("el registro ya existe con un tipo incompatible (%s)", r.Type)
			}
			if r.Type == recordType && r.Name == fullRecordName {
				return fmt.Errorf("el registro %s tipo %s ya existe", fullRecordName, recordType)
			}
		}
	}

	body := map[string]interface{}{
		"type":    recordType,
		"name":    fullRecordName,
		"content": ip,
		"proxied": proxied,
		"ttl":     1,
	}

	var createResp cfDNSRecordResponse
	err = p.request(ctx, "POST", fmt.Sprintf("/zones/%s/dns_records", zoneID), nil, body, &createResp)
	if err != nil {
		return fmt.Errorf("error de la API de Cloudflare: %w", err)
	}

	return nil
}

func (p *CloudflareProvider) UpdateDomains(ctx context.Context, ip string, managedZones []config.ManagedZone) error {
	recordType := "A"
	if strings.Contains(ip, ":") {
		recordType = "AAAA"
	}

	g, ctx := errgroup.WithContext(ctx)

	for _, mz := range managedZones {
		mz := mz
		g.Go(func() error {
			zoneID, err := p.zoneIDByName(ctx, mz.Domain)
			if err != nil {
				console.LogError("Fallo al obtener Zone ID para %s: %v", mz.Domain, err)
				return nil
			}

			recordG, ctx := errgroup.WithContext(ctx)

			for _, recordName := range mz.Records {
				recordName := recordName
				recordG.Go(func() error {
					fullRecordName := mz.Domain
					if recordName != "@" && recordName != mz.Domain {
						fullRecordName = fmt.Sprintf("%s.%s", recordName, mz.Domain)
					}

					var listResp cfDNSRecordListResponse
					err = p.request(ctx, "GET", fmt.Sprintf("/zones/%s/dns_records", zoneID), map[string]string{
						"name": fullRecordName,
						"type": recordType,
					}, nil, &listResp)
					if err != nil {
						console.LogError("Fallo al consultar registro %s: %v", fullRecordName, err)
						return nil
					}

					if len(listResp.Result) == 0 {
						console.LogWait("Registro %s tipo %s no encontrado en %s. Omitiendo.", fullRecordName, recordType, mz.Domain)
						return nil
					}

					record := listResp.Result[0]

					if record.Content == ip {
						console.LogInfo("Registro %s ya está actualizado (%s).", fullRecordName, ip)
						return nil
					}

					body := map[string]interface{}{
						"type":    recordType,
						"name":    record.Name,
						"content": ip,
						"proxied": record.Proxied,
						"ttl":     record.TTL,
					}

					updateCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
					defer cancel()

					var updateResp cfDNSRecordResponse
					err = p.request(updateCtx, "PUT", fmt.Sprintf("/zones/%s/dns_records/%s", zoneID, record.ID), nil, body, &updateResp)
					if err != nil {
						console.LogError("Fallo al actualizar registro %s: %v", fullRecordName, err)
					} else {
						console.LogSuccess("Actualizado %s apuntando a %s", fullRecordName, ip)
					}

					return nil
				})
			}

			return recordG.Wait()
		})
	}

	return g.Wait()
}
