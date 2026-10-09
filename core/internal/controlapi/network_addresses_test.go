package controlapi

import (
	"context"
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"github.com/QuantumNous/astrlink/core/contract"
	"github.com/QuantumNous/astrlink/core/internal/storage/sqlite"
)

func TestNetworkAddressesKeepReachableUnicastAddressesIPv4First(t *testing.T) {
	cidr := func(value string) net.Addr {
		_, network, err := net.ParseCIDR(value)
		if err != nil {
			t.Fatal(err)
		}
		ip, _, _ := net.ParseCIDR(value)
		network.IP = ip
		return network
	}
	interfaces := []networkInterface{
		{name: "lo0", up: true, loopback: true, addresses: []net.Addr{cidr("127.0.0.1/8"), cidr("::1/128")}},
		{name: "utun3", up: true, addresses: []net.Addr{cidr("fd7a:115c:a1e0::1/48"), cidr("100.101.102.103/32"), cidr("fe80::1/64")}},
		{name: "en0", up: true, addresses: []net.Addr{cidr("2001:db8::10/64"), cidr("192.168.1.20/24"), cidr("169.254.5.5/16")}},
		{name: "en1", up: false, addresses: []net.Addr{cidr("10.0.0.9/8")}},
		{name: "bridge0", up: true, addresses: []net.Addr{&net.IPAddr{IP: net.IPv4zero}, cidr("ff02::1/16")}},
	}
	got := networkAddresses(interfaces)
	want := []contract.NetworkAddress{
		{Interface: "en0", IP: "192.168.1.20"},
		{Interface: "utun3", IP: "100.101.102.103"},
		{Interface: "en0", IP: "2001:db8::10"},
		{Interface: "utun3", IP: "fd7a:115c:a1e0::1"},
	}
	if len(got) != len(want) {
		t.Fatalf("addresses = %+v, want %+v", got, want)
	}
	for index := range want {
		if got[index] != want[index] {
			t.Fatalf("addresses[%d] = %+v, want %+v", index, got[index], want[index])
		}
	}
	if empty := networkAddresses(nil); empty == nil || len(empty) != 0 {
		t.Fatalf("no interfaces = %#v, want an empty list", empty)
	}
}

func TestNetworkAddressesRouteNeedsTheOperatorToken(t *testing.T) {
	store, err := sqlite.Open(context.Background(), filepath.Join(t.TempDir(), "astrlink.db"))
	if err != nil {
		t.Fatalf("open SQLite: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })
	handler, err := NewWithDependencies(contract.DefaultVersionResponse("0.1.0-test", "abc1234"), Dependencies{
		ServiceStore: store, ControlToken: testControlToken, ObserverToken: testObserverToken,
	})
	if err != nil {
		t.Fatalf("NewWithDependencies: %v", err)
	}
	get := func(target, token string) *httptest.ResponseRecorder {
		request := httptest.NewRequest(http.MethodGet, target, nil)
		if token != "" {
			request.Header.Set("Authorization", "Bearer "+token)
		}
		recorder := httptest.NewRecorder()
		handler.ServeHTTP(recorder, request)
		return recorder
	}

	response := get(NetworkAddressesPath, testControlToken)
	if response.Code != http.StatusOK || response.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("status=%d headers=%v body=%s", response.Code, response.Header(), response.Body.String())
	}
	var body contract.NetworkAddressesResponse
	if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode: %v (%s)", err, response.Body.String())
	}
	if body.Addresses == nil {
		t.Fatalf("addresses missing: %s", response.Body.String())
	}
	for _, address := range body.Addresses {
		if ip := net.ParseIP(address.IP); ip == nil || ip.IsLoopback() || ip.IsLinkLocalUnicast() || address.Interface == "" {
			t.Fatalf("unexpected address %+v", address)
		}
	}
	if response := get(NetworkAddressesPath+"?all=1", testControlToken); response.Code != http.StatusBadRequest {
		t.Fatalf("query status=%d", response.Code)
	}
	if response := get(NetworkAddressesPath, testObserverToken); response.Code == http.StatusOK {
		t.Fatalf("observer token status=%d, want refused", response.Code)
	}
	if response := get(NetworkAddressesPath, ""); response.Code == http.StatusOK {
		t.Fatalf("anonymous status=%d, want refused", response.Code)
	}
}
