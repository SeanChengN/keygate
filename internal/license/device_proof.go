package license

import (
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"strconv"
	"strings"
	"time"
)

const deviceProofVersion = "wms-device-proof:v1"

type DeviceProof struct {
	PublicKey string
	Timestamp int64
	Nonce     string
	Signature string
}

func deviceProofMessage(action, licenseKey, identifier, productID, platform, channel, version string, timestamp int64, nonce string) []byte {
	return []byte(strings.Join([]string{
		deviceProofVersion, action, licenseKey, identifier, productID,
		platform, channel, version, strconv.FormatInt(timestamp, 10), nonce,
	}, "\n"))
}

func VerifyDeviceProof(proof DeviceProof, action, licenseKey, identifier, productID, platform, channel, version string, now time.Time) error {
	publicKey, err := base64.RawURLEncoding.DecodeString(strings.TrimSpace(proof.PublicKey))
	if err != nil || len(publicKey) != ed25519.PublicKeySize {
		return fmt.Errorf("invalid device public key")
	}
	signature, err := base64.RawURLEncoding.DecodeString(strings.TrimSpace(proof.Signature))
	if err != nil || len(signature) != ed25519.SignatureSize {
		return fmt.Errorf("invalid device proof signature")
	}
	nonce, err := base64.RawURLEncoding.DecodeString(strings.TrimSpace(proof.Nonce))
	if err != nil || len(nonce) != 16 {
		return fmt.Errorf("invalid device proof nonce")
	}
	createdAt := time.Unix(proof.Timestamp, 0)
	if proof.Timestamp <= 0 || createdAt.Before(now.Add(-5*time.Minute)) || createdAt.After(now.Add(5*time.Minute)) {
		return fmt.Errorf("device proof timestamp is outside the allowed window")
	}
	message := deviceProofMessage(action, licenseKey, identifier, productID, platform, channel, version, proof.Timestamp, proof.Nonce)
	if !ed25519.Verify(ed25519.PublicKey(publicKey), message, signature) {
		return fmt.Errorf("invalid device proof")
	}
	return nil
}

func DevicePublicKeyFingerprint(encoded string) (string, error) {
	publicKey, err := base64.RawURLEncoding.DecodeString(strings.TrimSpace(encoded))
	if err != nil || len(publicKey) != ed25519.PublicKeySize {
		return "", fmt.Errorf("invalid device public key")
	}
	digest := sha256.Sum256(publicKey)
	return hex.EncodeToString(digest[:8]), nil
}
