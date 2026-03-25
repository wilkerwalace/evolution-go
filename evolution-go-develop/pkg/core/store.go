package core

import (
	"crypto/rand"
	"encoding/json"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strings"
)

const (
	runtimeDataFile = ".runtime.dat"
	instanceFile    = ".instance"
)

// RuntimeData persisted locally after registration.
type RuntimeData struct {
	APIKey     string `json:"k"`
	Tier       string `json:"t"`
	CustomerID int    `json:"c,omitempty"`
}

// loadRuntimeData reads the saved license from disk.
func loadRuntimeData() (*RuntimeData, error) {
	path := resolveDataPath(runtimeDataFile)
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var rd RuntimeData
	if err := json.Unmarshal(data, &rd); err != nil {
		return nil, err
	}
	if rd.APIKey == "" {
		return nil, fmt.Errorf("invalid runtime data")
	}
	return &rd, nil
}

// saveRuntimeData writes the license to disk.
func saveRuntimeData(rd *RuntimeData) error {
	path := resolveDataPath(runtimeDataFile)
	data, err := json.Marshal(rd)
	if err != nil {
		return err
	}
	return os.WriteFile(path, data, 0600)
}

// removeRuntimeData deletes the license file.
func removeRuntimeData() {
	os.Remove(resolveDataPath(runtimeDataFile))
}

// loadOrCreateInstanceID generates or loads a persistent instance ID based on hardware.
func loadOrCreateInstanceID() (string, error) {
	path := resolveDataPath(instanceFile)
	data, err := os.ReadFile(path)
	if err == nil {
		id := strings.TrimSpace(string(data))
		if len(id) == 36 {
			return id, nil
		}
	}

	// Generate hardware-based instance ID
	id := generateHardwareID()
	if id == "" {
		// Fallback to random UUID
		id, err = newUUID()
		if err != nil {
			return "", err
		}
	}

	if err := os.WriteFile(path, []byte(id), 0600); err != nil {
		return "", err
	}
	return id, nil
}

// generateHardwareID creates a deterministic ID from MAC + hostname.
func generateHardwareID() string {
	hostname, _ := os.Hostname()
	macAddr := getPrimaryMAC()
	if hostname == "" && macAddr == "" {
		return ""
	}

	seed := hostname + "|" + macAddr
	// Create a UUID-like format from the hash
	h := make([]byte, 16)
	copy(h, []byte(seed))
	// XOR fold if seed is longer
	for i := 16; i < len(seed); i++ {
		h[i%16] ^= seed[i]
	}
	h[6] = (h[6] & 0x0f) | 0x40 // version 4
	h[8] = (h[8] & 0x3f) | 0x80 // variant
	return fmt.Sprintf("%08x-%04x-%04x-%04x-%012x",
		h[0:4], h[4:6], h[6:8], h[8:10], h[10:16])
}

func getPrimaryMAC() string {
	interfaces, err := net.Interfaces()
	if err != nil {
		return ""
	}
	for _, iface := range interfaces {
		if iface.Flags&net.FlagLoopback != 0 || iface.Flags&net.FlagUp == 0 {
			continue
		}
		if len(iface.HardwareAddr) > 0 {
			return iface.HardwareAddr.String()
		}
	}
	return ""
}

func newUUID() (string, error) {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", err
	}
	b[6] = (b[6] & 0x0f) | 0x40
	b[8] = (b[8] & 0x3f) | 0x80
	return fmt.Sprintf("%08x-%04x-%04x-%04x-%012x",
		b[0:4], b[4:6], b[6:8], b[8:10], b[10:16]), nil
}

func resolveDataPath(filename string) string {
	// Use current working directory
	return filepath.Join(".", filename)
}
