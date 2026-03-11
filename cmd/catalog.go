package main

import (
	"fmt"

	consulapi "github.com/hashicorp/consul/api"
)

func (app *App) fetchCatalogServices() (map[string]ServiceMeta, error) {
	services := make(map[string]ServiceMeta)

	serviceList, _, err := app.catalogClient.Catalog().Services(nil)
	if err != nil {
		return nil, fmt.Errorf("error listing catalog services: %w", err)
	}
	app.lo.Debug("Fetched catalog service list", "count", len(serviceList))

	for serviceName := range serviceList {
		svcMeta, err := app.fetchCatalogServiceMeta(serviceName)
		if err != nil {
			return nil, err
		}
		if svcMeta != nil {
			key := EnsureFQDN(svcMeta.DNSName)
			if existing, ok := services[key]; ok && preferExistingCatalogService(existing, *svcMeta) {
				continue
			}
			services[key] = *svcMeta
		}
	}

	return services, nil
}

func (app *App) fetchCatalogServiceMeta(serviceName string) (*ServiceMeta, error) {
	entries, _, err := app.catalogClient.Catalog().Service(serviceName, "", nil)
	if err != nil {
		return nil, fmt.Errorf("error fetching catalog service detail for %s: %w", serviceName, err)
	}

	return serviceMetaFromCatalogEntries(serviceName, entries), nil
}

func serviceMetaFromCatalogEntries(serviceName string, entries []*consulapi.CatalogService) *ServiceMeta {
	if len(entries) == 0 {
		return nil
	}

	tags := entries[0].ServiceTags
	if len(tags) == 0 || !hasHostnameAnnotation(tags) {
		return nil
	}

	namespace := entries[0].Namespace
	if namespace == "" {
		namespace = "default"
	}
	jobID := entries[0].ServiceMeta["external-source-job"]
	if jobID == "" {
		jobID = entries[0].ServiceMeta["job"]
	}

	return &ServiceMeta{
		Name:      serviceName,
		Namespace: namespace,
		Job:       jobID,
		Tags:      tags,
		Addresses: uniqueCatalogAddresses(entries),
		DNSName:   getDNSNameFromTags(tags),
		IsProxy:   isCatalogProxyService(entries[0]),
	}
}

func isCatalogProxyService(entry *consulapi.CatalogService) bool {
	return entry.ServiceProxy != nil && entry.ServiceProxy.DestinationServiceName != ""
}

func preferExistingCatalogService(existing, candidate ServiceMeta) bool {
	return !existing.IsProxy && candidate.IsProxy
}
