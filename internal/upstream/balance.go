package upstream

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"github.com/great-magician-01/any-llm/internal/logger"
	"github.com/great-magician-01/any-llm/internal/model"
)

// Supported balance/quota vendors, identified by the upstream's base-URL host.
const (
	VendorDeepSeek   = "deepseek"
	VendorKimiCoding = "kimi-coding"
)

// balanceVendorHosts maps a base-URL hostname to its balance/quota vendor.
// It is a package variable (not a const map) so tests can register httptest
// server hosts; production code never mutates it after init.
var balanceVendorHosts = map[string]string{
	"api.deepseek.com": VendorDeepSeek,
	"api.kimi.com":     VendorKimiCoding,
}

// BalanceVendor reports which vendor-specific balance/quota API an upstream
// supports, by the host of its base URL. "" means unsupported.
func BalanceVendor(u *model.Upstream) string {
	parsed, err := url.Parse(u.BaseURL)
	if err != nil {
		return ""
	}
	return balanceVendorHosts[strings.ToLower(parsed.Hostname())]
}

// balanceURL builds the vendor endpoint URL from the upstream base URL's
// origin (scheme://host) plus a fixed path. endpointURL is not used: it
// inserts /v1 for anthropic-format upstreams, while these vendor APIs have
// their own path rules (DeepSeek's balance endpoint has no /v1 prefix).
func balanceURL(u *model.Upstream, vendor string) (string, error) {
	parsed, err := url.Parse(u.BaseURL)
	if err != nil {
		return "", fmt.Errorf("parse base url: %w", err)
	}
	origin := parsed.Scheme + "://" + parsed.Host
	switch vendor {
	case VendorDeepSeek:
		return origin + "/user/balance", nil
	case VendorKimiCoding:
		return origin + "/coding/v1/usages", nil
	}
	return "", fmt.Errorf("unsupported vendor: %s", vendor)
}

// FetchBalance calls the vendor's balance/quota endpoint and returns the
// vendor id plus a normalized JSON payload (kind=balance|quota). The vendor
// is derived from the upstream's base-URL host; unsupported upstreams return
// an error.
func FetchBalance(ctx context.Context, httpClient *http.Client, u *model.Upstream) (string, json.RawMessage, error) {
	vendor := BalanceVendor(u)
	if vendor == "" {
		return "", nil, fmt.Errorf("balance fetch not supported for upstream %q", u.Name)
	}
	url, err := balanceURL(u, vendor)
	if err != nil {
		return "", nil, err
	}
	body, err := fetchBalanceBody(ctx, httpClient, url, u)
	if err != nil {
		return "", nil, err
	}
	var payload json.RawMessage
	switch vendor {
	case VendorDeepSeek:
		payload, err = normalizeDeepSeekBalance(body)
	case VendorKimiCoding:
		payload, err = normalizeKimiCodingUsage(body)
	}
	if err != nil {
		logger.Error("fetch balance: normalize failed", "vendor", vendor, "upstream", u.Name, "err", err)
		return "", nil, err
	}
	return vendor, payload, nil
}

func fetchBalanceBody(ctx context.Context, httpClient *http.Client, url string, u *model.Upstream) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, "GET", url, nil)
	if err != nil {
		logger.Error("fetch balance: create request", "url", url, "err", err)
		return nil, fmt.Errorf("create fetch request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+u.APIKey)
	resp, err := httpClient.Do(req)
	if err != nil {
		logger.Error("fetch balance: request failed", "url", url, "upstream", u.Name, "err", err)
		return nil, fmt.Errorf("fetch balance: %w", err)
	}
	defer resp.Body.Close()
	// Cap the read (success and error paths alike): a misconfigured or
	// hostile endpoint must not exhaust memory. A partial read surfaces as a
	// decode error in the normalize step downstream.
	body, _ := io.ReadAll(io.LimitReader(resp.Body, maxFetchBody))
	if resp.StatusCode >= 400 {
		logger.Error("fetch balance: upstream error",
			"url", url,
			"upstream", u.Name,
			"status", resp.StatusCode,
			"body", truncateFetch(string(body), 512),
		)
		// Truncate in the returned error too: admin handlers relay it into
		// the JSON response, where a full vendor error page (HTML) is noise.
		return nil, fmt.Errorf("upstream %d: %s", resp.StatusCode, truncateFetch(string(body), 512))
	}
	return body, nil
}

// Normalized payload shapes (contract with the admin API / frontend).

type balanceEntry struct {
	Currency string `json:"currency"`
	Total    string `json:"total"`
	Granted  string `json:"granted"`
	ToppedUp string `json:"topped_up"`
}

type balancePayload struct {
	Kind        string         `json:"kind"` // "balance"
	IsAvailable bool           `json:"is_available"`
	Balances    []balanceEntry `json:"balances"`
}

type quotaWindow struct {
	ID          string `json:"id"`
	UsedPercent string `json:"used_percent"`
	ResetAt     string `json:"reset_at,omitempty"`
}

type quotaPayload struct {
	Kind    string        `json:"kind"` // "quota"
	Windows []quotaWindow `json:"windows"`
}

// normalizeDeepSeekBalance converts GET /user/balance into the balance
// payload. Amounts stay raw strings to avoid float precision loss.
func normalizeDeepSeekBalance(body []byte) (json.RawMessage, error) {
	var resp struct {
		IsAvailable  bool `json:"is_available"`
		BalanceInfos []struct {
			Currency        string `json:"currency"`
			TotalBalance    string `json:"total_balance"`
			GrantedBalance  string `json:"granted_balance"`
			ToppedUpBalance string `json:"topped_up_balance"`
		} `json:"balance_infos"`
	}
	if err := json.Unmarshal(body, &resp); err != nil {
		return nil, fmt.Errorf("decode deepseek balance: %w", err)
	}
	out := balancePayload{Kind: "balance", IsAvailable: resp.IsAvailable, Balances: make([]balanceEntry, 0, len(resp.BalanceInfos))}
	for _, b := range resp.BalanceInfos {
		out.Balances = append(out.Balances, balanceEntry{
			Currency: b.Currency,
			Total:    b.TotalBalance,
			Granted:  b.GrantedBalance,
			ToppedUp: b.ToppedUpBalance,
		})
	}
	payload, err := json.Marshal(out)
	if err != nil {
		return nil, fmt.Errorf("encode balance payload: %w", err)
	}
	return payload, nil
}

// normalizeKimiCodingUsage converts GET /coding/v1/usages into the quota
// payload: 5-hour rolling window (limits[0].detail), weekly quota (usage),
// and the monthly total pool (totalQuota, which has no reset time). Parsing
// is fault-tolerant: a window whose fields are missing or unparsable is
// skipped rather than failing the whole snapshot.
func normalizeKimiCodingUsage(body []byte) (json.RawMessage, error) {
	var resp struct {
		Usage  *kimiUsageDetail `json:"usage"`
		Limits []struct {
			Window struct {
				Duration int    `json:"duration"`
				TimeUnit string `json:"timeUnit"`
			} `json:"window"`
			Detail *kimiUsageDetail `json:"detail"`
		} `json:"limits"`
		TotalQuota *kimiUsageDetail `json:"totalQuota"`
	}
	if err := json.Unmarshal(body, &resp); err != nil {
		return nil, fmt.Errorf("decode kimi-coding usages: %w", err)
	}
	out := quotaPayload{Kind: "quota", Windows: make([]quotaWindow, 0, 3)}
	// 5-hour window: the 300-minute entry of limits[] (fall back to the first
	// entry if the duration metadata is absent/changed).
	var fiveHour *kimiUsageDetail
	for i := range resp.Limits {
		if resp.Limits[i].Detail == nil {
			continue
		}
		if fiveHour == nil {
			fiveHour = resp.Limits[i].Detail
		}
		if resp.Limits[i].Window.Duration == 300 && resp.Limits[i].Window.TimeUnit == "TIME_UNIT_MINUTE" {
			fiveHour = resp.Limits[i].Detail
			break
		}
	}
	if w, ok := fiveHour.window("five_hour"); ok {
		out.Windows = append(out.Windows, w)
	}
	if w, ok := resp.Usage.window("weekly"); ok {
		out.Windows = append(out.Windows, w)
	}
	if w, ok := resp.TotalQuota.window("monthly"); ok {
		// The monthly total pool exposes no reset time.
		w.ResetAt = ""
		out.Windows = append(out.Windows, w)
	}
	payload, err := json.Marshal(out)
	if err != nil {
		return nil, fmt.Errorf("encode quota payload: %w", err)
	}
	return payload, nil
}

// kimiUsageDetail mirrors the vendor's {limit, used, remaining, resetTime}
// shape; all numbers are strings in the wire format.
type kimiUsageDetail struct {
	Limit     string `json:"limit"`
	Used      string `json:"used"`
	Remaining string `json:"remaining"`
	ResetTime string `json:"resetTime"`
}

// window builds a normalized quota window from a vendor detail. ok is false
// when the detail is absent or its used/limit strings don't parse.
func (d *kimiUsageDetail) window(id string) (quotaWindow, bool) {
	if d == nil {
		return quotaWindow{}, false
	}
	used, err1 := strconv.ParseFloat(d.Used, 64)
	limit, err2 := strconv.ParseFloat(d.Limit, 64)
	if err1 != nil || err2 != nil || limit <= 0 {
		return quotaWindow{}, false
	}
	return quotaWindow{
		ID:          id,
		UsedPercent: strconv.FormatFloat(used/limit*100, 'f', 1, 64),
		ResetAt:     d.ResetTime,
	}, true
}
