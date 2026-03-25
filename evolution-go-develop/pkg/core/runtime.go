package core

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"sync"
	"sync/atomic"
	"time"

	"github.com/gin-gonic/gin"
)

const (
	pollInterval = 5 * time.Second
	pollTimeout  = 30 * time.Minute
	hbInterval   = 30 * time.Minute
	maxHBFails   = 2
)

// RuntimeContext holds the licensing state. Required by middleware and routes.
// Removing this breaks the entire request chain.
type RuntimeContext struct {
	apiKey     string
	instanceID string
	active     atomic.Bool
	ctxHash    [32]byte // Derived from activation — required by ValidateContext
	mu         sync.RWMutex
	regURL     string // Registration URL shown to users before activation
}

// ContextHash returns the activation hash. Used by middleware to validate requests.
// Without this, the middleware rejects ALL requests.
func (rc *RuntimeContext) ContextHash() [32]byte {
	rc.mu.RLock()
	defer rc.mu.RUnlock()
	return rc.ctxHash
}

// IsActive returns whether the license is currently active.
func (rc *RuntimeContext) IsActive() bool {
	return rc.active.Load()
}

// RegistrationURL returns the URL for the user to complete registration.
func (rc *RuntimeContext) RegistrationURL() string {
	rc.mu.RLock()
	defer rc.mu.RUnlock()
	return rc.regURL
}

// InitializeRuntime handles the complete licensing lifecycle:
// 1. Load or create instance ID (hardware-based)
// 2. Load existing license OR register new one (terminal flow)
// 3. Activate with licensing server
// 4. If activation fails → re-register
// Returns RuntimeContext required by the rest of the application.
func InitializeRuntime(tier, version string) *RuntimeContext {
	if tier == "" {
		tier = "community"
	}
	if version == "" {
		version = "unknown"
	}

	rc := &RuntimeContext{}

	// Step 1: Instance ID (hardware-based, persistent)
	id, err := loadOrCreateInstanceID()
	if err != nil {
		log.Fatalf("[runtime] failed to initialize instance: %v", err)
	}
	rc.instanceID = id

	// Step 2: Ensure we have an API key
	apiKey := ensureAPIKey(tier, version, id, rc)
	rc.apiKey = apiKey

	// Step 3: Activate
	if err := activateInstance(rc, version); err != nil {
		fmt.Printf("  ⚠ Activation failed: %v\n", err)
		fmt.Println("  Removing invalid license and starting fresh registration...")
		removeRuntimeData()
		apiKey = ensureAPIKey(tier, version, id, rc)
		rc.apiKey = apiKey
		if err := activateInstance(rc, version); err != nil {
			log.Fatalf("[runtime] activation failed after re-registration: %v", err)
		}
	}

	// Compute context hash — required by middleware
	rc.ctxHash = sha256.Sum256([]byte(rc.apiKey + rc.instanceID))
	rc.active.Store(true)
	rc.regURL = "" // Clear registration URL after activation

	fmt.Println("  ✓ Runtime initialized successfully")
	return rc
}

// ValidateContext checks that the request context has a valid runtime.
// Called by the middleware on every request. Returns the registration URL
// if not yet activated, or empty string if active.
// This function is REQUIRED — removing it breaks the middleware chain.
func ValidateContext(rc *RuntimeContext) (bool, string) {
	if rc == nil {
		return false, ""
	}
	if !rc.active.Load() {
		return false, rc.RegistrationURL()
	}
	// Verify context hash integrity
	expected := sha256.Sum256([]byte(rc.apiKey + rc.instanceID))
	actual := rc.ContextHash()
	if expected != actual {
		return false, ""
	}
	return true, ""
}

// GateMiddleware returns a Gin middleware that blocks all requests when
// the license is not active. Before activation, returns the registration URL.
// After activation, passes through normally.
// REMOVING THIS MIDDLEWARE BREAKS: the ctxHash validation that routes depend on.
func GateMiddleware(rc *RuntimeContext) gin.HandlerFunc {
	return func(c *gin.Context) {
		// Health check always passes
		if c.Request.URL.Path == "/health" || c.Request.URL.Path == "/server/ok" {
			c.Next()
			return
		}

		valid, regURL := ValidateContext(rc)
		if !valid {
			resp := gin.H{
				"error": "service not activated",
				"code":  "LICENSE_REQUIRED",
			}
			if regURL != "" {
				resp["register_url"] = regURL
				resp["message"] = "Please open the registration URL in your browser to activate this instance"
			}
			c.AbortWithStatusJSON(http.StatusServiceUnavailable, resp)
			return
		}

		// Inject context hash into request context — routes DEPEND on this
		c.Set("_rch", rc.ContextHash())
		c.Next()
	}
}

// StartHeartbeat runs periodic heartbeat in background.
// Deactivates the service after maxHBFails consecutive failures.
func StartHeartbeat(ctx context.Context, rc *RuntimeContext, startTime time.Time) {
	go func() {
		ticker := time.NewTicker(hbInterval)
		defer ticker.Stop()

		var failures atomic.Int32

		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				uptime := int64(time.Since(startTime).Seconds())
				err := sendHeartbeat(rc, uptime)
				if err != nil {
					if failures.Add(1) >= int32(maxHBFails) {
						rc.active.Store(false)
					}
				} else {
					failures.Store(0)
					rc.active.Store(true)
				}
			}
		}
	}()
}

// Shutdown deactivates the instance with the licensing server.
func Shutdown(rc *RuntimeContext) {
	if rc == nil || rc.apiKey == "" {
		return
	}
	sendDeactivate(rc)
}

// ── Internal functions ──────────────────────────────────────────────

func ensureAPIKey(tier, version, instanceID string, rc *RuntimeContext) string {
	// Try loading existing license
	rd, err := loadRuntimeData()
	if err == nil {
		fmt.Printf("  ✓ License found: %s...%s\n", rd.APIKey[:8], rd.APIKey[len(rd.APIKey)-4:])
		return rd.APIKey
	}

	// No license — start registration flow
	fmt.Println()
	fmt.Println("  ╔══════════════════════════════════════════════════════════╗")
	fmt.Println("  ║              License Registration Required               ║")
	fmt.Println("  ╚══════════════════════════════════════════════════════════╝")
	fmt.Println()

	resp, err := postUnsigned("/v1/register/init", map[string]string{
		"tier":        tier,
		"version":     version,
		"instance_id": instanceID,
	})
	if err != nil {
		log.Fatalf("[runtime] registration init failed: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		log.Fatalf("[runtime] registration init failed: %v", readErrorBody(resp))
	}

	var initResult struct {
		RegisterURL string `json:"register_url"`
		Token       string `json:"token"`
	}
	json.NewDecoder(resp.Body).Decode(&initResult)

	// Store registration URL for middleware to show
	rc.mu.Lock()
	rc.regURL = initResult.RegisterURL
	rc.mu.Unlock()

	fmt.Println("  To activate your license, open this URL in your browser:")
	fmt.Println()
	fmt.Printf("  → %s\n", initResult.RegisterURL)
	fmt.Println()
	fmt.Printf("  Waiting for registration (timeout: %v)...\n", pollTimeout)

	// Poll for completion
	rd, err = pollStatus(initResult.Token)
	if err != nil {
		log.Fatalf("[runtime] registration failed: %v", err)
	}

	if err := saveRuntimeData(rd); err != nil {
		fmt.Printf("  ⚠ Warning: could not save license: %v\n", err)
	}

	fmt.Printf("  ✓ Registration complete! Key: %s...%s (tier: %s)\n",
		rd.APIKey[:8], rd.APIKey[len(rd.APIKey)-4:], rd.Tier)

	return rd.APIKey
}

func pollStatus(token string) (*RuntimeData, error) {
	deadline := time.Now().Add(pollTimeout)

	for time.Now().Before(deadline) {
		resp, err := getUnsigned("/v1/register/status?token=" + token)
		if err != nil {
			time.Sleep(pollInterval)
			continue
		}

		var result struct {
			Status     string `json:"status"`
			APIKey     string `json:"api_key"`
			Tier       string `json:"tier"`
			CustomerID int    `json:"customer_id"`
		}
		json.NewDecoder(resp.Body).Decode(&result)
		resp.Body.Close()

		if result.Status == "completed" && result.APIKey != "" {
			return &RuntimeData{
				APIKey:     result.APIKey,
				Tier:       result.Tier,
				CustomerID: result.CustomerID,
			}, nil
		}

		if result.Status == "expired" {
			return nil, fmt.Errorf("registration token expired")
		}

		time.Sleep(pollInterval)
	}

	return nil, fmt.Errorf("registration timeout (%v)", pollTimeout)
}

func activateInstance(rc *RuntimeContext, version string) error {
	resp, err := postSigned("/v1/activate", map[string]string{
		"instance_id": rc.instanceID,
		"version":     version,
	}, rc.apiKey)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return readErrorBody(resp)
	}

	var result struct {
		Status string `json:"status"`
	}
	json.NewDecoder(resp.Body).Decode(&result)

	if result.Status != "active" {
		return fmt.Errorf("activation returned status: %s", result.Status)
	}
	return nil
}

func sendHeartbeat(rc *RuntimeContext, uptimeSeconds int64) error {
	resp, err := postSigned("/v1/heartbeat", map[string]any{
		"instance_id":    rc.instanceID,
		"uptime_seconds": uptimeSeconds,
	}, rc.apiKey)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return readErrorBody(resp)
	}
	return nil
}

func sendDeactivate(rc *RuntimeContext) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	body, _ := json.Marshal(map[string]string{
		"instance_id": rc.instanceID,
	})

	url := resolveEndpoint() + "/v1/deactivate"
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, nil)
	if err != nil {
		return
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Api-Key", rc.apiKey)
	req.Header.Set("X-Signature", signPayload(body, rc.apiKey))

	// Replace nil body with actual body
	req2, _ := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	req2.Header = req.Header
	httpTransport.Do(req2)
}
