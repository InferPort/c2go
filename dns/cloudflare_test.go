package dns

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"c2go/config"
)

func newTestProvider(t *testing.T, handler http.HandlerFunc) (*httptest.Server, *CloudflareProvider) {
	t.Helper()
	srv := httptest.NewServer(handler)

	origBaseURL := apiBaseURL
	origClient := httpClient
	apiBaseURL = srv.URL
	httpClient = &http.Client{}
	t.Cleanup(func() {
		apiBaseURL = origBaseURL
		httpClient = origClient
	})

	return srv, &CloudflareProvider{token: "test-token"}
}

func TestNewCloudflareProvider_EmptyToken(t *testing.T) {
	_, err := NewCloudflareProvider("")
	if err == nil {
		t.Error("expected error for empty token")
	}
}

func TestNewCloudflareProvider_ValidToken(t *testing.T) {
	p, err := NewCloudflareProvider("valid-token")
	if err != nil {
		t.Fatal(err)
	}
	if p.token != "valid-token" {
		t.Errorf("expected valid-token, got %s", p.token)
	}
}

func TestListZones_Success(t *testing.T) {
	_, p := newTestProvider(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer test-token" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		json.NewEncoder(w).Encode(map[string]interface{}{
			"success": true,
			"result": []map[string]interface{}{
				{"id": "zone1", "name": "example.com"},
				{"id": "zone2", "name": "test.org"},
			},
			"result_info": map[string]interface{}{
				"page":        1,
				"per_page":    100,
				"total_pages": 1,
				"count":       2,
				"total_count": 2,
			},
		})
	}))

	zones, err := p.ListZones(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(zones) != 2 {
		t.Errorf("expected 2 zones, got %d", len(zones))
	}
}

func TestListZones_Unauthorized(t *testing.T) {
	_, p := newTestProvider(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		json.NewEncoder(w).Encode(map[string]interface{}{
			"success": false,
			"errors":  []map[string]interface{}{{"code": 9103, "message": "Invalid access token"}},
		})
	}))

	_, err := p.ListZones(context.Background())
	if err == nil {
		t.Error("expected error for unauthorized token")
	}
}

func TestListZones_Pagination(t *testing.T) {
	allZones := make([]map[string]interface{}, 0, 250)
	for i := range 250 {
		allZones = append(allZones, map[string]interface{}{
			"id":   "zone-" + itoa(i),
			"name": "zone" + itoa(i) + ".example.com",
		})
	}

	pageNum := 0
	_, p := newTestProvider(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		pageNum++
		page := 1
		if p := r.URL.Query().Get("page"); p != "" {
			page = atoi(p)
		}
		start := (page - 1) * 100
		end := start + 100
		if end > 250 {
			end = 250
		}

		json.NewEncoder(w).Encode(map[string]interface{}{
			"success": true,
			"result":  allZones[start:end],
			"result_info": map[string]interface{}{
				"page":        page,
				"per_page":    100,
				"total_pages": 3,
				"count":       end - start,
				"total_count": 250,
			},
		})
	}))

	zones, err := p.ListZones(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(zones) != 250 {
		t.Errorf("expected 250 zones across pages, got %d", len(zones))
	}
	if pageNum != 3 {
		t.Errorf("expected 3 page requests, got %d", pageNum)
	}
}

func TestListARecords_Success(t *testing.T) {
	callCount := 0
	_, p := newTestProvider(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		callCount++
		if callCount == 1 {
			json.NewEncoder(w).Encode(map[string]interface{}{
				"success": true,
				"result": []map[string]interface{}{
					{"id": "zone1", "name": "example.com"},
				},
				"result_info": map[string]interface{}{
					"page": 1, "per_page": 100, "total_pages": 1, "count": 1, "total_count": 1,
				},
			})
			return
		}
		json.NewEncoder(w).Encode(map[string]interface{}{
			"success": true,
			"result": []map[string]interface{}{
				{"id": "rec1", "type": "A", "name": "www.example.com", "content": "1.2.3.4", "proxied": false, "ttl": 1},
				{"id": "rec2", "type": "AAAA", "name": "example.com", "content": "::1", "proxied": false, "ttl": 1},
			},
			"result_info": map[string]interface{}{
				"page": 1, "per_page": 100, "total_pages": 1, "count": 2, "total_count": 2,
			},
		})
	}))

	names, err := p.ListARecords(context.Background(), "example.com")
	if err != nil {
		t.Fatal(err)
	}
	if len(names) != 2 {
		t.Errorf("expected 2 records, got %d: %v", len(names), names)
	}
}

func TestUpdateDomains_AlreadyUpToDate(t *testing.T) {
	callCount := 0
	_, p := newTestProvider(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		callCount++
		if callCount == 1 {
			json.NewEncoder(w).Encode(map[string]interface{}{
				"success": true,
				"result": []map[string]interface{}{
					{"id": "zone1", "name": "example.com"},
				},
				"result_info": map[string]interface{}{
					"page": 1, "per_page": 100, "total_pages": 1, "count": 1, "total_count": 1,
				},
			})
			return
		}
		json.NewEncoder(w).Encode(map[string]interface{}{
			"success": true,
			"result": []map[string]interface{}{
				{"id": "rec1", "type": "A", "name": "test.example.com", "content": "5.6.7.8", "proxied": false, "ttl": 1},
			},
			"result_info": map[string]interface{}{
				"page": 1, "per_page": 100, "total_pages": 1, "count": 1, "total_count": 1,
			},
		})
	}))

	zones := []config.ManagedZone{
		{Domain: "example.com", Records: []string{"test"}},
	}

	err := p.UpdateDomains(context.Background(), "5.6.7.8", zones)
	if err != nil {
		t.Fatal(err)
	}
}

func TestUpdateDomains_UpdateRequired(t *testing.T) {
	callCount := 0
	_, p := newTestProvider(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		callCount++
		if callCount == 1 {
			json.NewEncoder(w).Encode(map[string]interface{}{
				"success": true,
				"result": []map[string]interface{}{
					{"id": "zone1", "name": "example.com"},
				},
				"result_info": map[string]interface{}{
					"page": 1, "per_page": 100, "total_pages": 1, "count": 1, "total_count": 1,
				},
			})
			return
		}
		if callCount <= 3 && r.Method == "GET" {
			json.NewEncoder(w).Encode(map[string]interface{}{
				"success": true,
				"result": []map[string]interface{}{
					{"id": "rec1", "type": "A", "name": "test.example.com", "content": "1.2.3.4", "proxied": false, "ttl": 1},
				},
				"result_info": map[string]interface{}{
					"page": 1, "per_page": 100, "total_pages": 1, "count": 1, "total_count": 1,
				},
			})
			return
		}
		if r.Method == "PUT" {
			json.NewEncoder(w).Encode(map[string]interface{}{
				"success": true,
				"result": map[string]interface{}{
					"id": "rec1", "type": "A", "name": "test.example.com", "content": "9.9.9.9", "proxied": false, "ttl": 1,
				},
			})
			return
		}
		w.WriteHeader(http.StatusInternalServerError)
	}))

	zones := []config.ManagedZone{
		{Domain: "example.com", Records: []string{"test"}},
	}

	err := p.UpdateDomains(context.Background(), "9.9.9.9", zones)
	if err != nil {
		t.Fatal(err)
	}
}

func TestRequest_FailureResponse(t *testing.T) {
	_, p := newTestProvider(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusTooManyRequests)
		json.NewEncoder(w).Encode(map[string]interface{}{
			"success": false,
			"errors":  []map[string]interface{}{{"code": 10000, "message": "Rate limit exceeded"}},
		})
	}))

	err := p.request(context.Background(), "GET", "/zones", nil, nil, nil)
	if err == nil {
		t.Error("expected error for rate limited response")
	}
}

func TestCreateARecord_AlreadyExists(t *testing.T) {
	callCount := 0
	_, p := newTestProvider(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		callCount++
		if callCount == 1 {
			json.NewEncoder(w).Encode(map[string]interface{}{
				"success": true,
				"result": []map[string]interface{}{
					{"id": "zone1", "name": "example.com"},
				},
				"result_info": map[string]interface{}{
					"page": 1, "per_page": 100, "total_pages": 1, "count": 1, "total_count": 1,
				},
			})
			return
		}
		json.NewEncoder(w).Encode(map[string]interface{}{
			"success": true,
			"result": []map[string]interface{}{
				{"id": "rec1", "type": "A", "name": "test.example.com", "content": "1.2.3.4", "proxied": false, "ttl": 1},
			},
			"result_info": map[string]interface{}{
				"page": 1, "per_page": 100, "total_pages": 1, "count": 1, "total_count": 1,
			},
		})
	}))

	err := p.CreateARecord(context.Background(), "example.com", "test", "5.6.7.8", false)
	if err == nil {
		t.Error("expected error for already existing record")
	}
}

func TestProviderInterfaceCompiles(t *testing.T) {
	var _ Provider = (*CloudflareProvider)(nil)
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var buf [20]byte
	i := len(buf)
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	return string(buf[i:])
}

func atoi(s string) int {
	n := 0
	for _, c := range s {
		if c < '0' || c > '9' {
			break
		}
		n = n*10 + int(c-'0')
	}
	return n
}
