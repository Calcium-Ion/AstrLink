package contract

// RecoveryPathID identifies a retired recovery path. It remains so historical
// routing settings and request recovery records stay readable.
type RecoveryPathID string

func (id RecoveryPathID) Validate() error { return ServiceID(id).Validate() }
