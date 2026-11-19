package xray

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestObservatoryPacesShortProbeIntervals(t *testing.T) {
	for _, interval := range []string{"-1s", "1ns"} {
		t.Run(interval, func(t *testing.T) {
			probes := make(chan struct{}, 2)
			target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				select {
				case probes <- struct{}{}:
				default:
				}
				w.WriteHeader(http.StatusNoContent)
			}))
			t.Cleanup(target.Close)

			config := fmt.Sprintf(`{
				"outbounds":[{"tag":"node","protocol":"freedom"}],
				"observatory":{"subjectSelector":["node"],"probeURL":%q,"probeInterval":%q}
			}`, target.URL, interval)
			p, err := ParseProfile([]byte(config))
			if err != nil {
				t.Fatal(err)
			}
			startCore(t, p)

			select {
			case <-probes:
			case <-time.After(5 * time.Second):
				t.Fatal("observer did not send its first probe")
			}
			select {
			case <-probes:
				t.Fatal("observer retried without allowing time between probes")
			case <-time.After(150 * time.Millisecond):
			}
		})
	}
}
