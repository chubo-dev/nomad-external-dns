package main

import (
	"testing"
	"time"

	"github.com/libdns/libdns"
	"github.com/stretchr/testify/require"
)

func TestGroupLibDNSRecords(t *testing.T) {
	grouped := groupLibDNSRecords([]libdns.Record{
		{Type: "A", Name: "api", Value: "10.0.0.10,10.0.0.11", TTL: 30 * time.Second},
		{Type: "TXT", Name: "api", Value: "owner=test", TTL: 30 * time.Second},
	})

	require.Len(t, grouped, 2)

	require.Equal(t, "A", grouped[0].recordType)
	require.Equal(t, "api", grouped[0].name)
	require.Len(t, grouped[0].records, 2)
	require.Equal(t, "10.0.0.10", grouped[0].records[0].Value)
	require.Equal(t, "10.0.0.11", grouped[0].records[1].Value)

	require.Equal(t, "TXT", string(grouped[1].recordType))
	require.Len(t, grouped[1].records, 1)
	require.Equal(t, "\"owner=test\"", grouped[1].records[0].Value)
}

func TestLibDNSRecordsFromHetznerRRSet(t *testing.T) {
	ttl := 45
	rrset := hetznerCloudRRSet{
		Name: "api.wizerd.dev",
		Type: "A",
		TTL:  &ttl,
		Records: []hetznerCloudRRSetRecord{
			{Value: "10.0.0.10"},
			{Value: "10.0.0.11"},
		},
	}

	records := libdnsRecordsFromHetznerRRSet("wizerd.dev", rrset)
	require.Len(t, records, 2)
	require.Equal(t, "api", records[0].Name)
	require.Equal(t, "10.0.0.10", records[0].Value)
	require.Equal(t, 45*time.Second, records[0].TTL)
	require.Equal(t, "api", records[1].Name)
	require.Equal(t, "10.0.0.11", records[1].Value)
}

func TestDurationToTTLClampsHetznerMinimum(t *testing.T) {
	ttl := durationToTTL(30 * time.Second)
	require.NotNil(t, ttl)
	require.Equal(t, defaultHetznerCloudMinTTLSeconds, *ttl)
}

func TestRelativeRRSetName(t *testing.T) {
	require.Equal(t, "", relativeRRSetName("@", "wizerd.dev"))
	require.Equal(t, "", relativeRRSetName("wizerd.dev", "wizerd.dev"))
	require.Equal(t, "api", relativeRRSetName("api.wizerd.dev", "wizerd.dev"))
	require.Equal(t, "api", relativeRRSetName("api", "wizerd.dev"))
}

func TestNormalizeRRSetName(t *testing.T) {
	require.Equal(t, "@", normalizeRRSetName(""))
	require.Equal(t, "@", normalizeRRSetName("@"))
	require.Equal(t, "api", normalizeRRSetName("api"))
}
