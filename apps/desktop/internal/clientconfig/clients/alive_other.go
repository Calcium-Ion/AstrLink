//go:build !unix

package clients

func alive(int) bool { return true }
