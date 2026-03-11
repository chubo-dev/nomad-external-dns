package main

import (
	"fmt"
	"net"
	"strings"
	"time"

	"github.com/libdns/libdns"
)

const (
	// HostnameAnnotationKey is the annotated tag for defining hostname.
	HostnameAnnotationKey = "external-dns/hostname"
	// TTLAnnotationKey is the annotated tag for defining TTL.
	TTLAnnotationKey = "external-dns/ttl"
	// TargetAnnotationKey overrides the discovered service addresses.
	TargetAnnotationKey = "external-dns/target"
	// DefaultTTL is the TTL to set for records if unspecified or unparseable.
	DefaultTTL = time.Second * 30
)

// ServiceMeta contains minimal items from a api.ServiceRegistration event.
type ServiceMeta struct {
	Name      string   // Human Name of the service.
	Namespace string   // Namespace to which the service belongs to.
	Job       string   // Job to which the service belongs to.
	Addresses []string // Address of all backend services which is fetched by calling Nomad HTTP API.
	Tags      []string // Tags in the given service.
	DNSName   string   // DNS name of the service.
	IsProxy   bool     // Whether the service represents a sidecar proxy registration.
}

// DNSProvider wraps the required libdns interfaces.
type DNSProvider interface {
	libdns.RecordAppender
	libdns.RecordGetter
	libdns.RecordSetter
	libdns.RecordDeleter
}

// RecordMeta wraps around `libdns.Record`
// and adds additional fields.
type RecordMeta struct {
	Records []libdns.Record
	Zone    string
}

func classifyTargets(targets []string) (string, []string, error) {
	if len(targets) == 0 {
		return "A", nil, nil
	}

	cleaned := make([]string, 0, len(targets))
	allIPs := true

	for _, target := range targets {
		target = strings.TrimSpace(target)
		if target == "" {
			continue
		}

		cleaned = append(cleaned, target)
		if net.ParseIP(target) == nil {
			allIPs = false
		}
	}

	if len(cleaned) == 0 {
		return "A", nil, nil
	}

	if allIPs {
		return "A", cleaned, nil
	}

	if len(cleaned) == 1 {
		return "CNAME", cleaned, nil
	}

	return "", nil, fmt.Errorf("non-IP external-dns targets must contain exactly one value")
}
