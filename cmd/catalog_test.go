package main

import (
	"testing"

	consulapi "github.com/hashicorp/consul/api"
	"github.com/stretchr/testify/require"
)

func TestServiceMetaFromCatalogEntries(t *testing.T) {
	t.Parallel()

	meta := serviceMetaFromCatalogEntries("web-http", []*consulapi.CatalogService{
		{
			Address:        "10.0.0.11",
			ServiceAddress: "172.26.64.10",
			ServiceName:    "web-http",
			ServiceTags: []string{
				"external-dns/hostname=coffeeshop.wizerd.dev",
				"external-dns/target=95.217.171.236",
				"traefik.enable=true",
			},
			ServiceMeta: map[string]string{
				"external-source-job": "go-coffeeshop",
			},
		},
		{
			Address:        "10.0.0.12",
			ServiceAddress: "172.26.64.11",
			ServiceName:    "web-http",
			ServiceTags: []string{
				"external-dns/hostname=coffeeshop.wizerd.dev",
				"external-dns/target=95.217.171.236",
			},
		},
	})

	require.NotNil(t, meta)
	require.Equal(t, "web-http", meta.Name)
	require.Equal(t, "default", meta.Namespace)
	require.Equal(t, "go-coffeeshop", meta.Job)
	require.Equal(t, "coffeeshop.wizerd.dev", meta.DNSName)
	require.ElementsMatch(t, []string{"172.26.64.10", "172.26.64.11"}, meta.Addresses)
	require.False(t, meta.IsProxy)
}

func TestServiceMetaFromCatalogEntriesWithoutHostnameAnnotation(t *testing.T) {
	t.Parallel()

	meta := serviceMetaFromCatalogEntries("web-http", []*consulapi.CatalogService{
		{
			Address:     "10.0.0.11",
			ServiceName: "web-http",
			ServiceTags: []string{"traefik.enable=true"},
		},
	})

	require.Nil(t, meta)
}

func TestPreferExistingCatalogService(t *testing.T) {
	t.Parallel()

	require.True(t, preferExistingCatalogService(
		ServiceMeta{Name: "web-http", IsProxy: false},
		ServiceMeta{Name: "web-http-sidecar-proxy", IsProxy: true},
	))
	require.False(t, preferExistingCatalogService(
		ServiceMeta{Name: "web-http-sidecar-proxy", IsProxy: true},
		ServiceMeta{Name: "web-http", IsProxy: false},
	))
}
