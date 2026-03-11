package main

import (
	"testing"

	"github.com/libdns/libdns"
	"github.com/stretchr/testify/require"
)

func TestGroupOwnedRecordsUsesFQDNKeys(t *testing.T) {
	t.Parallel()

	owned := map[string][]RecordMeta{}
	records := []libdns.Record{
		{
			Type:  "A",
			Name:  "coffeeshop",
			Value: "95.217.171.236",
		},
		{
			Type:  "TXT",
			Name:  "coffeeshop",
			Value: "service=go-coffeeshop-dns namespace=default owner=test-owner created-by=nomad-external-dns",
		},
	}

	groupOwnedRecords(&owned, records, []string{"coffeeshop"}, "wizerd.dev.")

	meta, ok := owned["coffeeshop.wizerd.dev."]
	require.True(t, ok)
	require.Len(t, meta, 2)
	require.Equal(t, "coffeeshop", meta[0].Records[0].Name)
	require.Equal(t, "wizerd.dev.", meta[0].Zone)
}
