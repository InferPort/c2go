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
	"https://1.1.1.1/cdn-cgi/trace",
	"https://api.ipify.org",
	"https://ifconfig.me/ip",
	"https://icanhazip.com",
}

var httpClient = &http.Client{
	Timeout: 5 * time.Second,
}

func GetPublicIP(ctx context.Context) (string, error) {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	type result struct {
		ip  string
		err error
	}
	results := make(chan result, len(providers))

	for _, url := range providers {
		url := url
		go func() {
			ip, err := fetchIP(ctx, url)
			select {
			case results <- result{ip, err}:
			case <-ctx.Done():
			}
		}()
	}

	for range providers {
		select {
		case res := <-results:
			if res.err == nil && isValidIP(res.ip) {
				cancel()
				return res.ip, nil
			}
		case <-ctx.Done():
			return "", ErrNoInternet
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

	bodyStr := string(bodyBytes)
	if strings.Contains(url, "cdn-cgi/trace") {
		lines := strings.Split(bodyStr, "\n")
		for _, line := range lines {
			if strings.HasPrefix(line, "ip=") {
				return strings.TrimSpace(strings.TrimPrefix(line, "ip=")), nil
			}
		}
		return "", errors.New("ip key not found in trace response")
	}

	return strings.TrimSpace(bodyStr), nil
}

func isValidIP(ip string) bool {
	return net.ParseIP(ip) != nil
}

type InterfaceInfo struct {
	Name  string   `json:"name"`
	IPs   []string `json:"ips"`
	Flags []string `json:"flags"`
	IsUp  bool     `json:"is_up"`
	MAC   string   `json:"mac"`
}

// GetNetworkInterfaces lists all non-loopback network interfaces with their local IPs
func GetNetworkInterfaces() ([]InterfaceInfo, error) {
	ifaces, err := net.Interfaces()
	if err != nil {
		return nil, err
	}

	var result []InterfaceInfo
	for _, iface := range ifaces {
		if iface.Flags&net.FlagLoopback != 0 {
			continue
		}

		addrs, err := iface.Addrs()
		if err != nil {
			continue
		}

		var ips []string
		for _, addr := range addrs {
			if ipNet, ok := addr.(*net.IPNet); ok && !ipNet.IP.IsLoopback() {
				ips = append(ips, ipNet.IP.String())
			}
		}

		var flagStrs []string
		if iface.Flags&net.FlagUp != 0 {
			flagStrs = append(flagStrs, "UP")
		}
		if iface.Flags&net.FlagBroadcast != 0 {
			flagStrs = append(flagStrs, "BROADCAST")
		}
		if iface.Flags&net.FlagPointToPoint != 0 {
			flagStrs = append(flagStrs, "P2P")
		}
		if iface.Flags&net.FlagMulticast != 0 {
			flagStrs = append(flagStrs, "MULTICAST")
		}

		result = append(result, InterfaceInfo{
			Name:  iface.Name,
			IPs:   ips,
			Flags: flagStrs,
			IsUp:  iface.Flags&net.FlagUp != 0 && len(ips) > 0,
			MAC:   iface.HardwareAddr.String(),
		})
	}

	return result, nil
}

// GetPublicIPWithInterfaces resolves public IP by checking preferred interfaces in priority order
func GetPublicIPWithInterfaces(ctx context.Context, preferredInterfaces []string) (string, error) {
	if len(preferredInterfaces) == 0 {
		return GetPublicIP(ctx)
	}

	for _, ifaceName := range preferredInterfaces {
		iface, err := net.InterfaceByName(ifaceName)
		if err != nil || iface.Flags&net.FlagUp == 0 {
			continue
		}

		addrs, err := iface.Addrs()
		if err != nil {
			continue
		}

		var localIP net.IP
		for _, addr := range addrs {
			if ipNet, ok := addr.(*net.IPNet); ok && !ipNet.IP.IsLoopback() {
				localIP = ipNet.IP
				break
			}
		}

		if localIP == nil {
			continue
		}

		// Create interface-bound client
		boundTransport := &http.Transport{
			DialContext: (&net.Dialer{
				LocalAddr: &net.TCPAddr{IP: localIP},
				Timeout:   3 * time.Second,
			}).DialContext,
			TLSHandshakeTimeout: 3 * time.Second,
		}
		boundClient := &http.Client{
			Transport: boundTransport,
			Timeout:   5 * time.Second,
		}

		// Try providers with bound client
		for _, url := range providers {
			req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
			if err != nil {
				continue
			}
			resp, err := boundClient.Do(req)
			if err != nil {
				continue
			}
			bodyBytes, err := io.ReadAll(resp.Body)
			resp.Body.Close()
			if err != nil || resp.StatusCode != http.StatusOK {
				continue
			}

			bodyStr := strings.TrimSpace(string(bodyBytes))
			if strings.Contains(url, "cdn-cgi/trace") {
				for _, line := range strings.Split(bodyStr, "\n") {
					if strings.HasPrefix(line, "ip=") {
						ip := strings.TrimSpace(strings.TrimPrefix(line, "ip="))
						if isValidIP(ip) {
							return ip, nil
						}
					}
				}
				continue
			}

			if isValidIP(bodyStr) {
				return bodyStr, nil
			}
		}
	}

	// Fallback to default route
	return GetPublicIP(ctx)
}
