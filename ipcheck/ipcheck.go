package ipcheck

import (
	"context"
	"errors"
	"io"
	"net/http"
	"strings"
	"time"
)

var (
	// ErrNoInternet is returned when all IP check services fail.
	ErrNoInternet = errors.New("no internet connection or all IP services failed")
)

var providers = []string{
	"https://api.ipify.org",
	"https://ifconfig.me/ip",
	"https://icanhazip.com",
}

// GetPublicIP tries to fetch the current public IP from multiple fallback providers.
func GetPublicIP(ctx context.Context) (string, error) {
	client := &http.Client{
		Timeout: 5 * time.Second,
	}

	for _, url := range providers {
		ip, err := fetchIP(ctx, client, url)
		if err == nil && isValidIP(ip) {
			return ip, nil
		}
	}

	return "", ErrNoInternet
}

func fetchIP(ctx context.Context, client *http.Client, url string) (string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return "", err
	}

	resp, err := client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return "", errors.New("non-200 status code")
	}

	bodyBytes, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", err
	}

	return strings.TrimSpace(string(bodyBytes)), nil
}

func isValidIP(ip string) bool {
	if ip == "" {
		return false
	}
	return strings.Contains(ip, ".") || strings.Contains(ip, ":")
}
