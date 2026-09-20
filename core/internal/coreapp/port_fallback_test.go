package coreapp

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/QuantumNous/astrlink/core/contract"
	"github.com/QuantumNous/astrlink/core/internal/endpoint"
	"github.com/QuantumNous/astrlink/core/internal/ingress"
)

func TestOccupiedInferencePortFallsBackAndKeepsProductionHostGate(t *testing.T) {
	occupied, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer occupied.Close()
	config := DefaultConfig("0.1.0-test", "abc1234")
	config.InferenceListen = occupied.Addr().String()
	config.InferencePortFallback = true
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	writer := newRecordingWriter()
	runErrors := make(chan error, 1)
	go func() {
		runErrors <- RunWithDependencies(ctx, config, writer, Dependencies{
			NewInferenceHandler: func(address string) (http.Handler, error) {
				return ingress.NewProduction(ingress.Dependencies{
					AllowedHost: address,
					Resolver:    endpoint.UnavailableResolver{},
					Authorizer:  endpoint.NewSecretAuthorizer(nil),
					AccessTokenAuthenticator: ingress.AccessTokenAuthenticatorFunc(func(context.Context, string) (contract.AccessTokenID, error) {
						return "", errors.New("unauthenticated")
					}),
				})
			},
		})
	}()
	t.Cleanup(func() {
		cancel()
		if err := waitForRunError(t, runErrors); err != nil {
			t.Errorf("shutdown: %v", err)
		}
	})
	waitForReadyWrite(t, writer)
	writer.mu.Lock()
	data := append([]byte(nil), writer.buffer.Bytes()...)
	writer.mu.Unlock()
	var ready contract.ReadyEvent
	if err := json.Unmarshal(data, &ready); err != nil {
		t.Fatal(err)
	}
	if err := ready.Validate(); err != nil {
		t.Fatal(err)
	}
	if ready.InferenceURL == "http://"+config.InferenceListen || ready.InferenceURL == ready.ControlURL {
		t.Fatalf("fallback did not get a separate port: %+v", ready)
	}
	client := &http.Client{Timeout: 2 * time.Second, Transport: &http.Transport{Proxy: nil}}
	defer client.CloseIdleConnections()
	for _, tc := range []struct {
		name, host, origin string
		status             int
	}{
		{name: "active host still requires a token", status: http.StatusUnauthorized},
		{name: "saved host is rejected", host: config.InferenceListen, status: http.StatusMisdirectedRequest},
		{name: "browser origin is rejected", origin: "https://example.com", status: http.StatusForbidden},
	} {
		t.Run(tc.name, func(t *testing.T) {
			request, _ := http.NewRequest(http.MethodGet, ready.InferenceURL+"/v1/models", nil)
			if tc.host != "" {
				request.Host = tc.host
			}
			if tc.origin != "" {
				request.Header.Set("Origin", tc.origin)
			}
			response, err := client.Do(request)
			if err != nil {
				t.Fatal(err)
			}
			defer response.Body.Close()
			if response.StatusCode != tc.status {
				t.Fatalf("status = %d, want %d", response.StatusCode, tc.status)
			}
		})
	}
	response, err := client.Get(ready.ControlURL + "/control/v1/health")
	if err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	if response.StatusCode != http.StatusOK {
		t.Fatalf("control health = %d", response.StatusCode)
	}
	// The process occupying the saved port is untouched.
	conn, err := net.DialTimeout("tcp", occupied.Addr().String(), time.Second)
	if err != nil {
		t.Fatal(err)
	}
	conn.Close()
}

func TestInferencePortFallbackIsOptInAndOnlyHandlesAddressInUse(t *testing.T) {
	for _, tc := range []struct {
		name     string
		enabled  bool
		firstErr error
		calls    int
	}{
		{"disabled", false, addressInUse, 1},
		{"permission denied", true, syscall.EACCES, 1},
		{"fallback also fails", true, addressInUse, 2},
	} {
		t.Run(tc.name, func(t *testing.T) {
			config := DefaultConfig("0.1.0-test", "abc1234")
			config.InferencePortFallback = tc.enabled
			calls := 0
			var ready bytes.Buffer
			err := run(context.Background(), config, &ready, func(network, address string) (net.Listener, error) {
				calls++
				want := config.InferenceListen
				cause := tc.firstErr
				if calls > 1 {
					want = "127.0.0.1:0"
					cause = syscall.EACCES
				}
				if network != "tcp" || address != want {
					t.Fatalf("unexpected bind: %s %s", network, address)
				}
				return nil, &net.OpError{Op: "listen", Net: network, Err: cause}
			})
			if err == nil || calls != tc.calls || ready.Len() != 0 {
				t.Fatalf("error = %v, binds = %d, ready = %s", err, calls, ready.String())
			}
			if tc.calls == 2 && !strings.Contains(err.Error(), "fallback port") {
				t.Fatalf("missing fallback failure detail: %v", err)
			}
		})
	}
}

func TestInferenceHandlerSetupFailureClosesBothListenersWithoutReady(t *testing.T) {
	config := DefaultConfig("0.1.0-test", "abc1234")
	inference := newBlockingListener(config.InferenceListen)
	control := newBlockingListener("127.0.0.1:54321")
	listeners := []*blockingListener{inference, control}
	var ready bytes.Buffer
	forced := errors.New("invalid production gate")
	err := runWithDependencies(context.Background(), config, &ready, func(_, _ string) (net.Listener, error) {
		listener := listeners[0]
		listeners = listeners[1:]
		return listener, nil
	}, Dependencies{NewInferenceHandler: func(address string) (http.Handler, error) {
		if address != config.InferenceListen {
			t.Fatalf("factory address = %s", address)
		}
		return nil, forced
	}})
	if !errors.Is(err, forced) || ready.Len() != 0 {
		t.Fatalf("error = %v, ready = %s", err, ready.String())
	}
	for _, listener := range []*blockingListener{inference, control} {
		select {
		case <-listener.closed:
		default:
			t.Fatal("listener leaked after handler setup failure")
		}
	}
}
