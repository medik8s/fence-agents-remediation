package redfish

import (
	"context"
	"encoding/pem"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"time"

	. "github.com/onsi/ginkgo/v2"
)

var _ = Describe("Redfish Actions metadata", func() {
	const target = testURI + "/Actions/ComputerSystem.Reset"
	const reset = `"#ComputerSystem.Reset":{"target":"` + target + `"}`
	const annotation = `"@odata.type":"#ComputerSystem.v1_10_0.Actions",`
	for _, mode := range []struct {
		name, power string
		force       bool
	}{
		{name: "On", power: "On", force: true},
		{name: "On read-only", power: "On"},
		{name: "Off", power: "Off", force: true},
		{name: "Off read-only", power: "Off"},
	} {
		for _, tc := range []struct {
			name, actions, wantErr string
			targetErr              bool
			annotatedRead          int32
			changeTarget           bool
		}{
			{name: "control", actions: `{` + reset + `}`},
			{name: "Actions annotation", actions: `{` + annotation + reset + `}`},
			{name: "annotation on second GET", actions: `{` + reset + `}`, annotatedRead: 2},
			{name: "annotation after POST", actions: `{` + reset + `}`, annotatedRead: 3},
			{name: "scalar Actions", actions: `"not an object"`, wantErr: "malformed Redfish actions"},
			{name: "array Actions", actions: `[]`, wantErr: "malformed Redfish actions"},
			{name: "scalar Reset", actions: `{` + annotation + `"#ComputerSystem.Reset":"not an action"}`, wantErr: "malformed Redfish actions"},
			{name: "array Reset", actions: `{` + annotation + `"#ComputerSystem.Reset":[]}`, wantErr: "malformed Redfish actions"},
			{name: "scalar target", actions: `{` + annotation + `"#ComputerSystem.Reset":{"target":17}}`, wantErr: "malformed Redfish actions"},
			{name: "duplicate annotation", actions: `{"@odata.type":"x","@odata.type":"y",` + reset + `}`, wantErr: "ambiguous Redfish response"},
			{name: "duplicate nested metadata", actions: `{` + annotation + `"Oem":{"nested":{"x":1,"x":2}},` + reset + `}`, wantErr: "ambiguous Redfish response"},
			{name: "case-aliased metadata", actions: `{` + annotation + `"Oem":{"nested":{"key":1,"KEY":2}},` + reset + `}`, wantErr: "ambiguous Redfish response"},
			{name: "Unicode-aliased metadata", actions: `{` + annotation + `"Oem":{"nested":[{"key":1,"Key":2}]},` + reset + `}`, wantErr: "ambiguous Redfish response"},
			{name: "duplicate Reset", actions: `{` + annotation + reset + `,` + reset + `}`, wantErr: "ambiguous Redfish response"},
			{name: "Unicode-aliased Reset", actions: `{` + annotation + reset + `,"#Computerſystem.Reset":{}}`, wantErr: "ambiguous Redfish response"},
			{name: "duplicate target", actions: `{` + annotation + `"#ComputerSystem.Reset":{"target":"` + target + `","target":"` + target + `"}}`, wantErr: "ambiguous Redfish response"},
			{name: "case-aliased target", actions: `{` + annotation + `"#ComputerSystem.Reset":{"target":"` + target + `","TARGET":"` + target + `"}}`, wantErr: "ambiguous Redfish response"},
			{name: "wrong system", actions: `{` + annotation + `"#ComputerSystem.Reset":{"target":"/redfish/v1/Systems/2/Actions/ComputerSystem.Reset"}}`, targetErr: true},
			{name: "wrong origin", actions: `{` + annotation + `"#ComputerSystem.Reset":{"target":"https://other.example` + target + `"}}`, targetErr: true},
			{name: "target changes on second GET", actions: `{` + annotation + reset + `}`, changeTarget: true},
		} {
			It(mode.name+"/"+tc.name, func() {
				t := GinkgoT()
				var gets, posts, guards atomic.Int32
				server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					defer GinkgoRecover()
					if r.Method == http.MethodPost {
						if r.URL.Path != target {
							t.Errorf("POST target = %q, want %q", r.URL.Path, target)
						}
						posts.Add(1)
						w.WriteHeader(http.StatusAccepted)
						return
					}
					if r.Method != http.MethodGet || r.URL.Path != testURI {
						t.Errorf("unexpected request %s %s", r.Method, r.URL.Path)
					}
					n := gets.Add(1)
					power, actions := mode.power, tc.actions
					if posts.Load() > 0 {
						power = "Off"
					}
					if n == tc.annotatedRead {
						actions = `{` + annotation + reset + `}`
					}
					if n == 2 && tc.changeTarget {
						actions = strings.ReplaceAll(actions, target, "/redfish/v1/Systems/2/Actions/ComputerSystem.Reset")
					}
					_, _ = fmt.Fprintf(w, `{"UUID":"%s","PowerState":"%s","Actions":%s}`, testUUID, power, actions)
				}))
				defer server.Close()
				cfg := Config{Endpoint: server.URL, SystemURI: testURI, SystemUUID: testUUID, CA: pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: server.Certificate().Raw}), Timeout: time.Second, PollInterval: time.Millisecond}
				err := VerifyOff(context.Background(), cfg, mode.force, func(context.Context) error {
					guards.Add(1)
					return nil
				})
				wantErr, wantPosts, wantGets := tc.wantErr, int32(0), int32(1)
				if wantErr == "" && mode.power == "On" {
					switch {
					case !mode.force:
						wantErr = "redfish system is not Off"
					case tc.targetErr:
						wantErr = "redfish reset target differs from bound system"
					case tc.changeTarget:
						wantErr, wantGets = "redfish reset target changed", 2
					default:
						wantPosts, wantGets = 1, 3
					}
				}
				if wantErr == "" && err != nil || wantErr != "" && (err == nil || !strings.Contains(err.Error(), wantErr)) {
					t.Errorf("error = %v, want %q", err, wantErr)
				}
				if gets.Load() != wantGets || posts.Load() != wantPosts || guards.Load() != wantPosts {
					t.Errorf("GET/POST/guard = %d/%d/%d, want %d/%d/%d", gets.Load(), posts.Load(), guards.Load(), wantGets, wantPosts, wantPosts)
				}
			})
		}
	}
})
