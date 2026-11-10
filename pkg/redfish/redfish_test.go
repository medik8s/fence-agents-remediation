package redfish

import (
	"context"
	"encoding/json"
	"encoding/pem"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

const testUUID = "00112233-4455-4677-8899-aabbccddeeff"
const testURI = "/redfish/v1/Systems/1"

func TestVerifyOff(t *testing.T) {
	for _, tc := range []struct {
		readOnly                                                                             bool
		noReset                                                                              bool
		name, uuid, power, target, raw, wantErrText                                          string
		status                                                                               int
		acceptedOn, alreadyOff, redirect, untrusted, deny, timeout, changedUUID, blockAction bool
		wantErr                                                                              bool
	}{
		{name: "off after accepted reset", uuid: testUUID, power: "On"},
		{name: "already off", uuid: testUUID, power: "Off", alreadyOff: true},
		{name: "wrong UUID", uuid: "aabbccdd-eeff-4011-8233-445566778899", power: "On", wantErr: true},
		{name: "missing UUID", power: "Off", wantErr: true},
		{name: "zero UUID", uuid: "00000000-0000-0000-0000-000000000000", power: "Off", wantErr: true},
		{name: "ones UUID", uuid: "ffffffff-ffff-ffff-ffff-ffffffffffff", power: "Off", wantErr: true},
		{name: "malformed UUID", uuid: "bad", power: "Off", wantErr: true},
		{name: "ambiguous UUID", raw: `{"UUID":"00112233-4455-4677-8899-aabbccddeeff","UUID":"00112233-4455-4677-8899-aabbccddeeff","PowerState":"Off"}`, wantErr: true},
		{name: "case-aliased UUID", raw: `{"UUID":"aabbccdd-eeff-4011-8233-445566778899","uuid":"00112233-4455-4677-8899-aabbccddeeff","PowerState":"Off"}`, wantErr: true},
		{name: "missing canonical UUID key", raw: `{"uuid":"00112233-4455-4677-8899-aabbccddeeff","PowerState":"Off"}`, wantErr: true},
		{name: "case-aliased power", raw: `{"UUID":"00112233-4455-4677-8899-aabbccddeeff","PowerState":"On","powerstate":"Off"}`, wantErr: true},
		{name: "Unicode-aliased power read-only", raw: `{"UUID":"00112233-4455-4677-8899-aabbccddeeff","PowerState":"On","Powerſtate":"Off"}`, readOnly: true, wantErr: true, wantErrText: "ambiguous Redfish response"},
		{name: "Unicode-aliased power action", raw: `{"UUID":"00112233-4455-4677-8899-aabbccddeeff","PowerState":"On","Powerſtate":"Off","Actions":{"#ComputerSystem.Reset":{"target":"/redfish/v1/Systems/1/Actions/ComputerSystem.Reset"}}}`, wantErr: true, wantErrText: "ambiguous Redfish response", noReset: true},
		{name: "Unicode-aliased Off read-only", raw: `{"UUID":"00112233-4455-4677-8899-aabbccddeeff","PowerState":"Off","Powerſtate":"On"}`, readOnly: true, wantErr: true, wantErrText: "ambiguous Redfish response"},
		{name: "malformed response", raw: `{`, wantErr: true},
		{name: "unknown power", uuid: testUUID, power: "PoweringOff", wantErr: true},
		{name: "accepted still on", uuid: testUUID, power: "On", acceptedOn: true, wantErr: true},
		{name: "cross system action", uuid: testUUID, power: "On", target: "/redfish/v1/Systems/2/Actions/ComputerSystem.Reset", wantErr: true},
		{name: "cross origin action", uuid: testUUID, power: "On", target: "https://other.example/redfish/v1/Systems/1/Actions/ComputerSystem.Reset", wantErr: true},
		{name: "redirect", redirect: true, wantErr: true},
		{name: "untrusted TLS", untrusted: true, wantErr: true},
		{name: "unauthorized", deny: true, wantErr: true},
		{name: "server error", status: http.StatusServiceUnavailable, wantErr: true},
		{name: "timeout", timeout: true, wantErr: true},
		{name: "UUID changed after reset", uuid: testUUID, power: "On", changedUUID: true, wantErr: true},
		{name: "target changed before reset", uuid: testUUID, power: "On", blockAction: true, wantErr: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var resets, redirected atomic.Int32
			server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == "/elsewhere" {
					redirected.Add(1)
					w.WriteHeader(http.StatusOK)
					return
				}
				if tc.timeout {
					<-r.Context().Done()
					return
				}
				if tc.redirect {
					http.Redirect(w, r, "/elsewhere", http.StatusFound)
					return
				}
				user, password, ok := r.BasicAuth()
				if tc.deny || !ok || user != "user" || password != "password" {
					w.WriteHeader(http.StatusUnauthorized)
					return
				}
				if tc.status != 0 {
					w.WriteHeader(tc.status)
					return
				}
				if r.Method == http.MethodPost {
					if r.URL.Path != testURI+"/Actions/ComputerSystem.Reset" {
						t.Error("wrong action URI")
					}
					var action struct{ ResetType string }
					if err := json.NewDecoder(r.Body).Decode(&action); err != nil || action.ResetType != "ForceOff" {
						t.Error("wrong reset action")
					}
					resets.Add(1)
					w.WriteHeader(http.StatusAccepted)
					return
				}
				if r.URL.Path != testURI {
					t.Error("wrong system URI")
				}
				if tc.raw != "" {
					_, _ = w.Write([]byte(tc.raw))
					return
				}
				uuid, power := tc.uuid, tc.power
				if resets.Load() > 0 {
					if !tc.acceptedOn {
						power = "Off"
					}
					if tc.changedUUID {
						uuid = "aabbccdd-eeff-4011-8233-445566778899"
					}
				}
				target := tc.target
				if target == "" {
					target = testURI + "/Actions/ComputerSystem.Reset"
				}
				_ = json.NewEncoder(w).Encode(map[string]any{"UUID": uuid, "PowerState": power, "Actions": map[string]any{"#ComputerSystem.Reset": map[string]string{"target": target}}})
			}))
			defer server.Close()
			ca := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: server.Certificate().Raw})
			if tc.untrusted {
				ca = nil
			}
			cfg := Config{Endpoint: server.URL, SystemURI: testURI, SystemUUID: testUUID, CA: ca, Username: "user", Password: "password", Timeout: 150 * time.Millisecond, PollInterval: time.Millisecond}
			force := !tc.readOnly
			var precondition func(context.Context) error
			if force {
				precondition = func(context.Context) error {
					if tc.blockAction {
						return errors.New("changed target")
					}
					return nil
				}
			}
			err := VerifyOff(context.Background(), cfg, force, precondition)
			if (err != nil) != tc.wantErr {
				t.Errorf("error = %v, wantErr %t", err, tc.wantErr)
			}
			if tc.wantErrText != "" && (err == nil || !strings.Contains(err.Error(), tc.wantErrText)) {
				t.Errorf("error = %v, want text %q", err, tc.wantErrText)
			}
			if tc.noReset && resets.Load() != 0 {
				t.Errorf("reset issued for ambiguous response: %d", resets.Load())
			}
			if tc.alreadyOff && resets.Load() != 0 {
				t.Error("reset already-off system")
			}
			if tc.blockAction && resets.Load() != 0 {
				t.Error("reset after failed precondition")
			}
			if redirected.Load() != 0 {
				t.Error("followed redirect")
			}
		})
	}
}

func TestUnavailableOrCanceled(t *testing.T) {
	for _, canceled := range []bool{false, true} {
		name := "unavailable"
		if canceled {
			name = "canceled"
		}
		t.Run(name, func(t *testing.T) {
			var requests atomic.Int32
			server := httptest.NewTLSServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { requests.Add(1) }))
			cfg := Config{Endpoint: server.URL, SystemURI: testURI, SystemUUID: testUUID, CA: pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: server.Certificate().Raw}), Timeout: 100 * time.Millisecond}
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			if canceled {
				cancel()
				defer server.Close()
			} else {
				server.Close()
			}
			if err := VerifyOff(ctx, cfg, true, func(context.Context) error { t.Error("action precondition after transport failure"); return nil }); err == nil {
				t.Fatal("transport failure accepted")
			}
			if requests.Load() != 0 {
				t.Fatal("transport failure sent BMC request")
			}
		})
	}
}

func TestReleaseCheckNeverResets(t *testing.T) {
	var resets atomic.Int32
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			resets.Add(1)
		}
		_ = json.NewEncoder(w).Encode(map[string]string{"UUID": testUUID, "PowerState": "On"})
	}))
	defer server.Close()
	cfg := Config{Endpoint: server.URL, SystemURI: testURI, SystemUUID: testUUID, CA: pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: server.Certificate().Raw}), Timeout: time.Second}
	if err := VerifyOff(context.Background(), cfg, false, nil); err == nil {
		t.Fatal("release accepted On")
	}
	if resets.Load() != 0 {
		t.Fatal("release check issued reset")
	}
}
