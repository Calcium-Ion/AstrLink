// Package keystore keeps the secrets the desktop holds for Core: the local
// key-encryption key (in the system keychain, or a file beside the data) and
// the pin of Core's raw content key. It ports kek_store.rs and raw_key_pin.rs.
package keystore
