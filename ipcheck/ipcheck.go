package ipcheck

import (
	"context"
	"errors"
	"io"
	"net"
	"net/http"
	"strings"
	"time"
)

var (
	ErrNoInternet = errors.New("no internet connection or all IP services failed")
)

var providers = []string{
	"https://api.ipify.org",
	"https://ifconfig.me/ip",
	"https://icanhazip.com",
}

var httpClient = &http.Client{
	Timeout: 5 * time.Second,
}

func GetPublicIP(ctx context.Context) (string, error) {
	for _, url := range providers {
		ip, err := fetchIP(ctx, url)
		if err == nil && isValidIP(ip) {
			return ip, nil
		}
	}

	return "", ErrNoInternet
}

func fetchIP(ctx context.Context, url string) (string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return "", err
	}

	resp, err := httpClient.Do(req)
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
	return net.ParseIP(ip) != nil
}
