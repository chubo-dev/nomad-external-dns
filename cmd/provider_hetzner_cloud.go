package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/libdns/libdns"
)

const (
	defaultHetznerCloudAPIEndpoint   = "https://api.hetzner.cloud/v1"
	defaultHetznerCloudPollInterval  = 500 * time.Millisecond
	defaultHetznerCloudActionTimeout = 30 * time.Second
	defaultHetznerCloudMinTTLSeconds = 60
)

type hetznerCloudProvider struct {
	client   *http.Client
	token    string
	endpoint string
}

func newHetznerCloudProvider(token string) (*hetznerCloudProvider, error) {
	if strings.TrimSpace(token) == "" {
		return nil, fmt.Errorf("missing Hetzner Cloud token")
	}

	return &hetznerCloudProvider{
		client:   &http.Client{Timeout: 15 * time.Second},
		token:    strings.TrimSpace(token),
		endpoint: defaultHetznerCloudAPIEndpoint,
	}, nil
}

func hetznerCloudTokenFromConfig() string {
	if token := strings.TrimSpace(os.Getenv("NOMAD_EXTERNAL_DNS_PROVIDER_HETZNER__TOKEN")); token != "" {
		return token
	}

	return strings.TrimSpace(os.Getenv("HCLOUD_TOKEN"))
}

func (p *hetznerCloudProvider) GetRecords(ctx context.Context, zone string) ([]libdns.Record, error) {
	zoneObj, err := p.getZone(ctx, zone)
	if err != nil {
		return nil, err
	}

	rrsets, err := p.listRRSets(ctx, zoneObj.ID)
	if err != nil {
		return nil, err
	}

	records := make([]libdns.Record, 0)
	for _, rrset := range rrsets {
		records = append(records, libdnsRecordsFromHetznerRRSet(zoneObj.Name, rrset)...)
	}

	return records, nil
}

func (p *hetznerCloudProvider) AppendRecords(ctx context.Context, zone string, records []libdns.Record) ([]libdns.Record, error) {
	return p.SetRecords(ctx, zone, records)
}

func (p *hetznerCloudProvider) SetRecords(ctx context.Context, zone string, records []libdns.Record) ([]libdns.Record, error) {
	zoneObj, err := p.getZone(ctx, zone)
	if err != nil {
		return nil, err
	}

	grouped := groupLibDNSRecords(records)
	applied := make([]libdns.Record, 0, len(records))

	for _, rrsetInput := range grouped {
		existing, err := p.getRRSet(ctx, zoneObj.ID, rrsetInput.name, rrsetInput.recordType)
		if err != nil {
			return nil, err
		}

		if existing == nil {
			actionID, err := p.createRRSet(ctx, zoneObj.ID, rrsetInput)
			if err != nil {
				return nil, err
			}
			if err := p.waitForAction(ctx, actionID); err != nil {
				return nil, err
			}
		} else {
			if ttlChanged(existing.TTL, rrsetInput.ttl) {
				actionID, err := p.changeRRSetTTL(ctx, zoneObj.ID, rrsetInput.name, rrsetInput.recordType, rrsetInput.ttl)
				if err != nil {
					return nil, err
				}
				if err := p.waitForAction(ctx, actionID); err != nil {
					return nil, err
				}
			}

			actionID, err := p.setRRSetRecords(ctx, zoneObj.ID, rrsetInput.name, rrsetInput.recordType, rrsetInput.records)
			if err != nil {
				return nil, err
			}
			if err := p.waitForAction(ctx, actionID); err != nil {
				return nil, err
			}
		}

		applied = append(applied, rrsetInput.libdnsRecords...)
	}

	return applied, nil
}

func (p *hetznerCloudProvider) DeleteRecords(ctx context.Context, zone string, records []libdns.Record) ([]libdns.Record, error) {
	zoneObj, err := p.getZone(ctx, zone)
	if err != nil {
		return nil, err
	}

	grouped := groupLibDNSRecords(records)
	deleted := make([]libdns.Record, 0, len(records))

	for _, rrsetInput := range grouped {
		existing, err := p.getRRSet(ctx, zoneObj.ID, rrsetInput.name, rrsetInput.recordType)
		if err != nil {
			return nil, err
		}
		if existing == nil {
			continue
		}

		actionID, err := p.deleteRRSet(ctx, zoneObj.ID, rrsetInput.name, rrsetInput.recordType)
		if err != nil {
			return nil, err
		}
		if err := p.waitForAction(ctx, actionID); err != nil {
			return nil, err
		}

		deleted = append(deleted, rrsetInput.libdnsRecords...)
	}

	return deleted, nil
}

func (p *hetznerCloudProvider) getZone(ctx context.Context, zone string) (*hetznerCloudZone, error) {
	zoneName := strings.TrimSuffix(strings.TrimSpace(zone), ".")
	if zoneName == "" {
		return nil, fmt.Errorf("missing zone name")
	}

	var response hetznerCloudListZonesResponse
	if err := p.request(ctx, http.MethodGet, "/zones?name="+url.QueryEscape(zoneName), nil, &response); err != nil {
		return nil, err
	}

	for _, zoneObj := range response.Zones {
		if zoneObj.Name == zoneName {
			return &zoneObj, nil
		}
	}

	return nil, fmt.Errorf("zone %q not found in Hetzner Cloud DNS", zoneName)
}

func (p *hetznerCloudProvider) listRRSets(ctx context.Context, zoneID int64) ([]hetznerCloudRRSet, error) {
	var response hetznerCloudListRRSetsResponse
	if err := p.request(ctx, http.MethodGet, fmt.Sprintf("/zones/%d/rrsets", zoneID), nil, &response); err != nil {
		return nil, err
	}

	return response.RRSets, nil
}

func (p *hetznerCloudProvider) getRRSet(ctx context.Context, zoneID int64, name, recordType string) (*hetznerCloudRRSet, error) {
	var response hetznerCloudRRSetResponse
	err := p.request(ctx, http.MethodGet, fmt.Sprintf("/zones/%d/rrsets/%s/%s", zoneID, url.PathEscape(name), url.PathEscape(recordType)), nil, &response)
	if err != nil {
		if apiErr, ok := err.(*hetznerCloudAPIError); ok && apiErr.StatusCode == http.StatusNotFound {
			return nil, nil
		}
		return nil, err
	}

	return response.RRSet, nil
}

func (p *hetznerCloudProvider) createRRSet(ctx context.Context, zoneID int64, rrset groupedRRSet) (int64, error) {
	body := map[string]interface{}{
		"name":    rrset.name,
		"type":    rrset.recordType,
		"ttl":     rrset.ttl,
		"records": rrset.records,
	}

	var response hetznerCloudActionResponse
	if err := p.request(ctx, http.MethodPost, fmt.Sprintf("/zones/%d/rrsets", zoneID), body, &response); err != nil {
		return 0, err
	}

	return response.Action.ID, nil
}

func (p *hetznerCloudProvider) setRRSetRecords(ctx context.Context, zoneID int64, name, recordType string, records []hetznerCloudRRSetRecord) (int64, error) {
	var response hetznerCloudActionResponse
	if err := p.request(ctx, http.MethodPost, fmt.Sprintf("/zones/%d/rrsets/%s/%s/actions/set_records", zoneID, url.PathEscape(name), url.PathEscape(recordType)), map[string]interface{}{
		"records": records,
	}, &response); err != nil {
		return 0, err
	}

	return response.Action.ID, nil
}

func (p *hetznerCloudProvider) changeRRSetTTL(ctx context.Context, zoneID int64, name, recordType string, ttl *int) (int64, error) {
	var response hetznerCloudActionResponse
	if err := p.request(ctx, http.MethodPost, fmt.Sprintf("/zones/%d/rrsets/%s/%s/actions/change_ttl", zoneID, url.PathEscape(name), url.PathEscape(recordType)), map[string]interface{}{
		"ttl": ttl,
	}, &response); err != nil {
		return 0, err
	}

	return response.Action.ID, nil
}

func (p *hetznerCloudProvider) deleteRRSet(ctx context.Context, zoneID int64, name, recordType string) (int64, error) {
	var response hetznerCloudActionResponse
	if err := p.request(ctx, http.MethodDelete, fmt.Sprintf("/zones/%d/rrsets/%s/%s", zoneID, url.PathEscape(name), url.PathEscape(recordType)), nil, &response); err != nil {
		return 0, err
	}

	return response.Action.ID, nil
}

func (p *hetznerCloudProvider) waitForAction(ctx context.Context, actionID int64) error {
	if actionID == 0 {
		return nil
	}

	waitCtx, cancel := context.WithTimeout(ctx, defaultHetznerCloudActionTimeout)
	defer cancel()

	ticker := time.NewTicker(defaultHetznerCloudPollInterval)
	defer ticker.Stop()

	for {
		action, err := p.getAction(waitCtx, actionID)
		if err != nil {
			return err
		}

		switch action.Status {
		case "success":
			return nil
		case "error":
			if action.Error != nil && action.Error.Message != "" {
				return fmt.Errorf("Hetzner Cloud action %d failed: %s", actionID, action.Error.Message)
			}
			return fmt.Errorf("Hetzner Cloud action %d failed", actionID)
		}

		select {
		case <-waitCtx.Done():
			return fmt.Errorf("timed out waiting for Hetzner Cloud action %d: %w", actionID, waitCtx.Err())
		case <-ticker.C:
		}
	}
}

func (p *hetznerCloudProvider) getAction(ctx context.Context, actionID int64) (*hetznerCloudAction, error) {
	var response hetznerCloudActionResponse
	if err := p.request(ctx, http.MethodGet, fmt.Sprintf("/actions/%d", actionID), nil, &response); err != nil {
		return nil, err
	}

	return response.Action, nil
}

func (p *hetznerCloudProvider) request(ctx context.Context, method, path string, payload interface{}, out interface{}) error {
	var body io.Reader
	if payload != nil {
		data, err := json.Marshal(payload)
		if err != nil {
			return err
		}
		body = bytes.NewReader(data)
	}

	req, err := http.NewRequestWithContext(ctx, method, p.endpoint+path, body)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+p.token)
	if payload != nil {
		req.Header.Set("Content-Type", "application/json")
	}

	resp, err := p.client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	responseBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return err
	}

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		apiErr := &hetznerCloudAPIError{StatusCode: resp.StatusCode}
		var errorResponse hetznerCloudErrorResponse
		if len(responseBody) > 0 && json.Unmarshal(responseBody, &errorResponse) == nil {
			apiErr.Message = errorResponse.Error.Message
			apiErr.Code = errorResponse.Error.Code
		}
		if apiErr.Message == "" {
			apiErr.Message = strings.TrimSpace(string(responseBody))
		}
		return apiErr
	}

	if out == nil || len(responseBody) == 0 {
		return nil
	}

	return json.Unmarshal(responseBody, out)
}

type groupedRRSet struct {
	name          string
	recordType    string
	ttl           *int
	records       []hetznerCloudRRSetRecord
	libdnsRecords []libdns.Record
}

func groupLibDNSRecords(records []libdns.Record) []groupedRRSet {
	type key struct {
		name string
		typ  string
	}

	grouped := map[key]*groupedRRSet{}

	for _, record := range records {
		recordType := strings.ToUpper(strings.TrimSpace(record.Type))
		recordName := normalizeRRSetName(record.Name)
		k := key{name: recordName, typ: recordType}

		entry, ok := grouped[k]
		if !ok {
			entry = &groupedRRSet{
				name:       recordName,
				recordType: recordType,
				ttl:        durationToTTL(record.TTL),
			}
			grouped[k] = entry
		}

		entry.libdnsRecords = append(entry.libdnsRecords, record)
		entry.records = append(entry.records, hetznerRRSetRecordsFromLibDNSRecord(record)...)
	}

	keys := make([]key, 0, len(grouped))
	for k := range grouped {
		keys = append(keys, k)
	}
	sort.Slice(keys, func(i, j int) bool {
		if keys[i].name == keys[j].name {
			return keys[i].typ < keys[j].typ
		}
		return keys[i].name < keys[j].name
	})

	result := make([]groupedRRSet, 0, len(keys))
	for _, k := range keys {
		result = append(result, *grouped[k])
	}

	return result
}

func hetznerRRSetRecordsFromLibDNSRecord(record libdns.Record) []hetznerCloudRRSetRecord {
	values := []string{record.Value}
	if strings.EqualFold(record.Type, "A") || strings.EqualFold(record.Type, "AAAA") {
		values = splitRecordValues(record.Value)
	}

	out := make([]hetznerCloudRRSetRecord, 0, len(values))
	for _, value := range values {
		out = append(out, hetznerCloudRRSetRecord{Value: encodeHetznerRRSetRecordValue(record.Type, value)})
	}

	return out
}

func libdnsRecordsFromHetznerRRSet(zoneName string, rrset hetznerCloudRRSet) []libdns.Record {
	name := relativeRRSetName(rrset.Name, zoneName)
	ttl := ttlToDuration(rrset.TTL)
	out := make([]libdns.Record, 0, len(rrset.Records))

	for _, record := range rrset.Records {
		out = append(out, libdns.Record{
			Type:  rrset.Type,
			Name:  name,
			Value: decodeHetznerRRSetRecordValue(rrset.Type, record.Value),
			TTL:   ttl,
		})
	}

	return out
}

func normalizeRRSetName(name string) string {
	trimmed := strings.TrimSuffix(strings.TrimSpace(name), ".")
	if trimmed == "" || trimmed == "@" {
		return "@"
	}
	return trimmed
}

func relativeRRSetName(name, zone string) string {
	trimmedName := strings.TrimSuffix(strings.TrimSpace(name), ".")
	trimmedZone := strings.TrimSuffix(strings.TrimSpace(zone), ".")

	switch {
	case trimmedName == "", trimmedName == "@", trimmedName == trimmedZone:
		return ""
	case strings.HasSuffix(trimmedName, "."+trimmedZone):
		return strings.TrimSuffix(trimmedName, "."+trimmedZone)
	default:
		return trimmedName
	}
}

func splitRecordValues(value string) []string {
	parts := strings.Split(value, ",")
	out := make([]string, 0, len(parts))
	for _, part := range parts {
		trimmed := strings.TrimSpace(part)
		if trimmed == "" {
			continue
		}
		out = append(out, trimmed)
	}
	if len(out) == 0 {
		return []string{strings.TrimSpace(value)}
	}
	return out
}

func durationToTTL(ttl time.Duration) *int {
	seconds := int(ttl.Seconds())
	if seconds <= 0 {
		seconds = int(DefaultTTL.Seconds())
	}
	if seconds < defaultHetznerCloudMinTTLSeconds {
		seconds = defaultHetznerCloudMinTTLSeconds
	}
	return &seconds
}

func ttlToDuration(ttl *int) time.Duration {
	if ttl == nil || *ttl <= 0 {
		return DefaultTTL
	}
	return time.Duration(*ttl) * time.Second
}

func ttlChanged(current, desired *int) bool {
	switch {
	case current == nil && desired == nil:
		return false
	case current == nil || desired == nil:
		return true
	default:
		return *current != *desired
	}
}

func encodeHetznerRRSetRecordValue(recordType, value string) string {
	if strings.EqualFold(recordType, "TXT") {
		return strconv.Quote(value)
	}

	return value
}

func decodeHetznerRRSetRecordValue(recordType, value string) string {
	if !strings.EqualFold(recordType, "TXT") {
		return value
	}

	unquoted, err := strconv.Unquote(value)
	if err != nil {
		return value
	}

	return unquoted
}

type hetznerCloudZone struct {
	ID   int64  `json:"id"`
	Name string `json:"name"`
}

type hetznerCloudRRSet struct {
	ID      string                    `json:"id"`
	Name    string                    `json:"name"`
	Type    string                    `json:"type"`
	TTL     *int                      `json:"ttl"`
	Records []hetznerCloudRRSetRecord `json:"records"`
}

type hetznerCloudRRSetRecord struct {
	Value string `json:"value"`
}

type hetznerCloudAction struct {
	ID     int64                    `json:"id"`
	Status string                   `json:"status"`
	Error  *hetznerCloudActionError `json:"error"`
}

type hetznerCloudActionError struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

type hetznerCloudListZonesResponse struct {
	Zones []hetznerCloudZone `json:"zones"`
}

type hetznerCloudListRRSetsResponse struct {
	RRSets []hetznerCloudRRSet `json:"rrsets"`
}

type hetznerCloudRRSetResponse struct {
	RRSet *hetznerCloudRRSet `json:"rrset"`
}

type hetznerCloudActionResponse struct {
	Action *hetznerCloudAction `json:"action"`
}

type hetznerCloudErrorResponse struct {
	Error struct {
		Code    string `json:"code"`
		Message string `json:"message"`
	} `json:"error"`
}

type hetznerCloudAPIError struct {
	StatusCode int
	Code       string
	Message    string
}

func (e *hetznerCloudAPIError) Error() string {
	if e == nil {
		return ""
	}
	if e.Code != "" && e.Message != "" {
		return fmt.Sprintf("Hetzner Cloud API error (%s): %s", e.Code, e.Message)
	}
	if e.Message != "" {
		return fmt.Sprintf("Hetzner Cloud API error: %s", e.Message)
	}
	return fmt.Sprintf("Hetzner Cloud API returned HTTP %d", e.StatusCode)
}
