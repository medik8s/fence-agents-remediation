package redfish

import (
	"bytes"
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"
	"unicode"
)

var uuidPattern = regexp.MustCompile(`^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$`)
var systemPathPattern = regexp.MustCompile(`^/redfish/v1/Systems/[A-Za-z0-9_~-][A-Za-z0-9._~-]*$`)

// Config fixes the authenticated system endpoint for one bounded verification.
type Config struct {
	Endpoint     string
	SystemURI    string
	SystemUUID   string
	CA           []byte
	Username     string
	Password     string
	Timeout      time.Duration
	PollInterval time.Duration
}

// ValidSystemUUID rejects absent and uninitialized SMBIOS identifiers.
func ValidSystemUUID(uuid string) bool {
	return uuidPattern.MatchString(uuid) && uuid != "00000000-0000-0000-0000-000000000000" && strings.ToLower(uuid) != "ffffffff-ffff-ffff-ffff-ffffffffffff"
}

// ValidateTarget requires an explicit ComputerSystem, not collection discovery.
func ValidateTarget(endpoint, systemURI string) error {
	u, err := url.Parse(endpoint)
	if err != nil || u.Scheme != "https" || u.Host == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" || u.Opaque != "" || (u.Path != "" && u.Path != "/") || u.RawPath != "" {
		return fmt.Errorf("invalid HTTPS Redfish endpoint")
	}
	if !systemPathPattern.MatchString(systemURI) {
		return fmt.Errorf("explicit ComputerSystem URI required")
	}
	return nil
}

type computerSystem struct {
	UUID       string `json:"UUID"`
	PowerState string `json:"PowerState"`
	Actions    map[string]struct {
		Target string `json:"target"`
	} `json:"Actions"`
}

// VerifyOff reports only same-system Off; force permits a guarded ForceOff action.
func VerifyOff(ctx context.Context, cfg Config, force bool, beforeAction func(context.Context) error) error {
	if err := ValidateTarget(cfg.Endpoint, cfg.SystemURI); err != nil {
		return err
	}
	if !ValidSystemUUID(cfg.SystemUUID) || cfg.Timeout <= 0 || cfg.Timeout > time.Minute {
		return fmt.Errorf("invalid Redfish identity or timeout")
	}
	ctx, cancel := context.WithTimeout(ctx, cfg.Timeout)
	defer cancel()
	var roots *x509.CertPool
	if len(cfg.CA) > 0 {
		roots = x509.NewCertPool()
		if !roots.AppendCertsFromPEM(cfg.CA) {
			return fmt.Errorf("invalid Redfish CA")
		}
	}
	transport := &http.Transport{TLSClientConfig: &tls.Config{RootCAs: roots, MinVersion: tls.VersionTLS12}, Proxy: nil}
	defer transport.CloseIdleConnections()
	client := &http.Client{Transport: transport, Timeout: cfg.Timeout, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	endpoint := strings.TrimSuffix(cfg.Endpoint, "/")
	read := func() (*computerSystem, error) {
		body, err := request(ctx, client, cfg, http.MethodGet, endpoint+cfg.SystemURI, nil)
		if err != nil {
			return nil, err
		}
		if err := uniqueJSON(body); err != nil {
			return nil, fmt.Errorf("ambiguous Redfish response")
		}
		var fields map[string]json.RawMessage
		if err := json.Unmarshal(body, &fields); err != nil || fields["UUID"] == nil || fields["PowerState"] == nil {
			return nil, fmt.Errorf("canonical redfish identity and power fields required")
		}
		var uuid, power string
		if err := json.Unmarshal(fields["UUID"], &uuid); err != nil {
			return nil, fmt.Errorf("malformed Redfish system UUID")
		}
		if err := json.Unmarshal(fields["PowerState"], &power); err != nil {
			return nil, fmt.Errorf("malformed Redfish power state")
		}
		var actions map[string]struct {
			Target string `json:"target"`
		}
		if fields["Actions"] != nil {
			if err := json.Unmarshal(fields["Actions"], &actions); err != nil {
				return nil, fmt.Errorf("malformed Redfish actions")
			}
		}
		system := &computerSystem{UUID: uuid, PowerState: power, Actions: actions}
		if !ValidSystemUUID(system.UUID) || !strings.EqualFold(system.UUID, cfg.SystemUUID) {
			return nil, fmt.Errorf("redfish system UUID mismatch")
		}
		if system.PowerState != "On" && system.PowerState != "Off" {
			return nil, fmt.Errorf("unknown Redfish power state")
		}
		return system, nil
	}
	system, err := read()
	if err != nil {
		return err
	}
	if system.PowerState == "Off" {
		return nil
	}
	if !force {
		return fmt.Errorf("redfish system is not Off")
	}
	action, found := system.Actions["#ComputerSystem.Reset"]
	if !found {
		return fmt.Errorf("redfish reset action missing")
	}
	allowedAction := cfg.SystemURI + "/Actions/ComputerSystem.Reset"
	if action.Target != allowedAction && action.Target != endpoint+allowedAction {
		return fmt.Errorf("redfish reset target differs from bound system")
	}
	if beforeAction == nil {
		return fmt.Errorf("redfish pre-action validation missing")
	}
	system, err = read()
	if err != nil {
		return err
	}
	if system.PowerState == "Off" {
		return nil
	}
	if current, ok := system.Actions["#ComputerSystem.Reset"]; !ok || current.Target != action.Target {
		return fmt.Errorf("redfish reset target changed")
	}
	if err := beforeAction(ctx); err != nil {
		return err
	}
	if _, err := request(ctx, client, cfg, http.MethodPost, endpoint+allowedAction, []byte(`{"ResetType":"ForceOff"}`)); err != nil {
		return err
	}
	interval := cfg.PollInterval
	if interval <= 0 {
		interval = 200 * time.Millisecond
	}
	for {
		system, err = read()
		if err != nil {
			return err
		}
		if system.PowerState == "Off" {
			return nil
		}
		timer := time.NewTimer(interval)
		select {
		case <-ctx.Done():
			timer.Stop()
			return fmt.Errorf("redfish Off confirmation timed out: %w", ctx.Err())
		case <-timer.C:
		}
	}
}

func request(ctx context.Context, client *http.Client, cfg Config, method, target string, body []byte) (data []byte, requestErr error) {
	req, err := http.NewRequestWithContext(ctx, method, target, bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("construct Redfish request")
	}
	req.SetBasicAuth(cfg.Username, cfg.Password)
	req.Header.Set("Accept", "application/json")
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("authenticated Redfish request failed")
	}
	defer func() {
		if err := resp.Body.Close(); err != nil && requestErr == nil {
			requestErr = fmt.Errorf("close redfish response: %w", err)
		}
	}()
	if method == http.MethodGet && resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("redfish GET rejected: HTTP %d", resp.StatusCode)
	}
	if method == http.MethodPost {
		if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusAccepted && resp.StatusCode != http.StatusNoContent {
			return nil, fmt.Errorf("redfish reset rejected: HTTP %d", resp.StatusCode)
		}
		return nil, nil
	}
	data, err = io.ReadAll(io.LimitReader(resp.Body, (1<<20)+1))
	if err != nil || len(data) > 1<<20 {
		return nil, fmt.Errorf("invalid Redfish response size")
	}
	return data, nil
}

func foldJSONName(name string) string {
	var folded strings.Builder
	folded.Grow(len(name))
	for _, r := range name {
		for {
			next := unicode.SimpleFold(r)
			if next <= r {
				folded.WriteRune(next)
				break
			}
			r = next
		}
	}
	return folded.String()
}

func uniqueJSON(data []byte) error {
	decoder := json.NewDecoder(bytes.NewReader(data))
	var visit func(int) error
	visit = func(depth int) error {
		if depth > 32 {
			return fmt.Errorf("JSON nesting limit")
		}
		token, err := decoder.Token()
		if err != nil {
			return err
		}
		switch token {
		case json.Delim('{'):
			seen := map[string]bool{}
			for decoder.More() {
				key, err := decoder.Token()
				if err != nil {
					return err
				}
				name, ok := key.(string)
				name = strings.ToLower(foldJSONName(name))
				if !ok || seen[name] {
					return fmt.Errorf("duplicate JSON key")
				}
				seen[name] = true
				if err := visit(depth + 1); err != nil {
					return err
				}
			}
			_, err = decoder.Token()
			return err
		case json.Delim('['):
			for decoder.More() {
				if err := visit(depth + 1); err != nil {
					return err
				}
			}
			_, err = decoder.Token()
			return err
		}
		return nil
	}
	if err := visit(0); err != nil {
		return err
	}
	if _, err := decoder.Token(); err != io.EOF {
		return fmt.Errorf("trailing JSON")
	}
	return nil
}
