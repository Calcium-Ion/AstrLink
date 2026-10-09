package controlapi

import (
	"net"
	"net/http"
	"slices"
	"strings"

	"github.com/QuantumNous/astrlink/core/contract"
)

// NetworkAddressesPath lists the addresses other machines reach this host by
// while the inference plane answers every interface. It is read on each call,
// so a changed Wi-Fi network or a VPN that came up later shows its current
// address.
const NetworkAddressesPath = "/control/v1/network-addresses"

// maxNetworkAddresses bounds the response on hosts with many virtual interfaces.
const maxNetworkAddresses = 64

func (handler *Handler) getNetworkAddresses(writer http.ResponseWriter, request *http.Request) {
	if request.URL.RawQuery != "" {
		writeError(writer, http.StatusBadRequest, "invalid_query", "network addresses do not accept query parameters")
		return
	}
	interfaces, err := systemInterfaces()
	if err != nil {
		writeError(writer, http.StatusInternalServerError, "network_addresses_unavailable", "network interfaces could not be read")
		return
	}
	writeJSON(writer, http.StatusOK, contract.NetworkAddressesResponse{Addresses: networkAddresses(interfaces)})
}

// networkInterface is the part of net.Interface that networkAddresses reads.
type networkInterface struct {
	name      string
	up        bool
	loopback  bool
	addresses []net.Addr
}

func systemInterfaces() ([]networkInterface, error) {
	system, err := net.Interfaces()
	if err != nil {
		return nil, err
	}
	interfaces := make([]networkInterface, 0, len(system))
	for _, current := range system {
		addresses, err := current.Addrs()
		if err != nil {
			continue
		}
		interfaces = append(interfaces, networkInterface{
			name:      current.Name,
			up:        current.Flags&net.FlagUp != 0,
			loopback:  current.Flags&net.FlagLoopback != 0,
			addresses: addresses,
		})
	}
	return interfaces, nil
}

// networkAddresses keeps the unicast addresses another machine can connect
// to: no loopback, link-local, multicast or unspecified addresses. IPv4 comes
// first because that is what people type into a client.
func networkAddresses(interfaces []networkInterface) []contract.NetworkAddress {
	result := make([]contract.NetworkAddress, 0)
	for _, current := range interfaces {
		if !current.up || current.loopback {
			continue
		}
		for _, address := range current.addresses {
			ip := addressIP(address)
			if ip == nil || ip.IsLoopback() || ip.IsLinkLocalUnicast() || ip.IsMulticast() || ip.IsUnspecified() {
				continue
			}
			result = append(result, contract.NetworkAddress{Interface: current.name, IP: ip.String()})
		}
	}
	slices.SortStableFunc(result, func(a, b contract.NetworkAddress) int {
		if aIPv4, bIPv4 := strings.Contains(a.IP, "."), strings.Contains(b.IP, "."); aIPv4 != bIPv4 {
			if aIPv4 {
				return -1
			}
			return 1
		}
		if byName := strings.Compare(a.Interface, b.Interface); byName != 0 {
			return byName
		}
		return strings.Compare(a.IP, b.IP)
	})
	if len(result) > maxNetworkAddresses {
		result = result[:maxNetworkAddresses]
	}
	return result
}

func addressIP(address net.Addr) net.IP {
	switch typed := address.(type) {
	case *net.IPNet:
		return typed.IP
	case *net.IPAddr:
		return typed.IP
	}
	return nil
}
