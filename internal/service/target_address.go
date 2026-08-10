package service

import (
	"fmt"
	"net"
	"net/netip"
	"strconv"
	"strings"
	"unicode"

	"golang.org/x/net/idna"
)

// ParsedTarget is a validated forwarding destination. IP is invalid for DNS
// targets and valid for literal IPv4/IPv6 targets.
type ParsedTarget struct {
	Host       string
	Port       int
	IP         netip.Addr
	Normalized string
}

// ParseTargetAddress validates and canonicalizes one host:port forwarding
// destination. nftables callers must require a literal IP so untrusted host
// text can never reach a generated rule.
func ParseTargetAddress(raw string, requireLiteralIP bool) (ParsedTarget, error) {
	if raw == "" {
		return ParsedTarget{}, fmt.Errorf("target address is empty")
	}
	if hasUnsafeTargetRune(raw) {
		return ParsedTarget{}, fmt.Errorf("target address contains unsafe characters")
	}

	host, portText, err := net.SplitHostPort(raw)
	if err != nil {
		return ParsedTarget{}, fmt.Errorf("invalid target address: %w", err)
	}
	if host == "" {
		return ParsedTarget{}, fmt.Errorf("target host is empty")
	}
	if portText == "" || strings.IndexFunc(portText, func(r rune) bool { return r < '0' || r > '9' }) >= 0 {
		return ParsedTarget{}, fmt.Errorf("target port must be a decimal number")
	}
	port, err := strconv.Atoi(portText)
	if err != nil || port < 1 || port > 65535 {
		return ParsedTarget{}, fmt.Errorf("target port must be between 1 and 65535")
	}

	parsed := ParsedTarget{Port: port}
	if addr, parseErr := netip.ParseAddr(host); parseErr == nil {
		if strings.HasPrefix(raw, "[") && !addr.Is6() {
			return ParsedTarget{}, fmt.Errorf("only IPv6 target hosts may use brackets")
		}
		if requireLiteralIP && addr.Zone() != "" {
			return ParsedTarget{}, fmt.Errorf("nft target IP zones are not supported")
		}
		parsed.IP = addr
		parsed.Host = addr.String()
	} else {
		if strings.HasPrefix(raw, "[") {
			return ParsedTarget{}, fmt.Errorf("DNS target hosts must not use brackets")
		}
		if requireLiteralIP {
			return ParsedTarget{}, fmt.Errorf("nft target must be a literal IP address")
		}
		asciiHost, asciiErr := idna.Lookup.ToASCII(host)
		if asciiErr != nil {
			return ParsedTarget{}, fmt.Errorf("invalid target DNS name: %w", asciiErr)
		}
		asciiHost = strings.ToLower(asciiHost)
		if !isStrictDNSName(asciiHost) {
			return ParsedTarget{}, fmt.Errorf("invalid target DNS name")
		}
		parsed.Host = asciiHost
	}

	parsed.Normalized = net.JoinHostPort(parsed.Host, strconv.Itoa(parsed.Port))
	return parsed, nil
}

func parseTargetHostPort(host string, port int, requireLiteralIP bool) (ParsedTarget, error) {
	if strings.HasPrefix(host, "[") && strings.HasSuffix(host, "]") {
		inner := strings.TrimSuffix(strings.TrimPrefix(host, "["), "]")
		if addr, err := netip.ParseAddr(inner); err == nil && addr.Is6() {
			host = inner
		}
	}
	return ParseTargetAddress(net.JoinHostPort(host, strconv.Itoa(port)), requireLiteralIP)
}

func normalizeGostRemoteAddresses(raw string) (string, error) {
	targets := splitRemoteAddresses(raw)
	if len(targets) == 0 {
		return "", fmt.Errorf("target address is empty")
	}
	normalized := make([]string, 0, len(targets))
	for _, target := range targets {
		parsed, err := ParseTargetAddress(target, false)
		if err != nil {
			return "", err
		}
		normalized = append(normalized, parsed.Normalized)
	}
	return strings.Join(normalized, ","), nil
}

func hasUnsafeTargetRune(raw string) bool {
	return strings.IndexFunc(raw, func(r rune) bool {
		if unicode.IsControl(r) || unicode.IsSpace(r) {
			return true
		}
		switch r {
		case ';', '#', '"', '\'':
			return true
		default:
			return false
		}
	}) >= 0
}

func isStrictDNSName(host string) bool {
	if host == "" || len(host) > 253 {
		return false
	}
	for _, label := range strings.Split(host, ".") {
		if len(label) < 1 || len(label) > 63 || label[0] == '-' || label[len(label)-1] == '-' {
			return false
		}
		for _, r := range label {
			if (r < 'a' || r > 'z') && (r < '0' || r > '9') && r != '-' {
				return false
			}
		}
	}
	return true
}
