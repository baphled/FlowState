package store

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"os"
	"strings"
)

// leaseIdentityMessage is the fixed, application-specific message the machine id keys (ADR-011).
const leaseIdentityMessage = "flowstate-lease-v1"

// hostName is the host name seam; tests replace it.
var hostName = os.Hostname

// MachineIdentity returns the value a lease row stores in its machine column:
// hex(HMAC-SHA256(key = the system machine id, message = "flowstate-lease-v1")),
// so the raw, confidential id is never exposed and the value is stable per machine.
// When no id can be read it returns "host:<host name>", the unstable fallback the
// ADR confines to machines without an id (a minimal container). It returns ""
// only when neither an id nor a host name is available.
func MachineIdentity() string {
	if id := strings.TrimSpace(readMachineID()); id != "" {
		return hashMachineID(id)
	}
	name, err := hostName()
	if err != nil || strings.TrimSpace(name) == "" {
		return ""
	}
	return "host:" + name
}

// hashMachineID keys HMAC-SHA256 with the id over the fixed message, as
// sd_id128_get_machine_app_specific(3) does.
func hashMachineID(id string) string {
	mac := hmac.New(sha256.New, []byte(id))
	mac.Write([]byte(leaseIdentityMessage))
	return hex.EncodeToString(mac.Sum(nil))
}
