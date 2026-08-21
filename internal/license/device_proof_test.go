package license

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"testing"
	"time"
)

func TestVerifyDeviceProofBindsActionAndDeviceKey(t *testing.T) {
	publicKey, privateKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 8, 21, 0, 0, 0, 0, time.UTC)
	nonceBytes := make([]byte, 16)
	if _, err := rand.Read(nonceBytes); err != nil {
		t.Fatal(err)
	}
	nonce := base64.RawURLEncoding.EncodeToString(nonceBytes)
	message := deviceProofMessage("verify", "KG-TEST", "site-test", "product-test", "", "", "", now.Unix(), nonce)
	proof := DeviceProof{
		PublicKey: base64.RawURLEncoding.EncodeToString(publicKey), Timestamp: now.Unix(), Nonce: nonce,
		Signature: base64.RawURLEncoding.EncodeToString(ed25519.Sign(privateKey, message)),
	}
	if err := VerifyDeviceProof(proof, "verify", "KG-TEST", "site-test", "product-test", "", "", "", now); err != nil {
		t.Fatalf("valid device proof rejected: %v", err)
	}
	if err := VerifyDeviceProof(proof, "download", "KG-TEST", "site-test", "product-test", "", "", "", now); err == nil {
		t.Fatal("proof replayed across actions must be rejected")
	}
	if err := VerifyDeviceProof(proof, "verify", "KG-TEST", "site-test", "product-test", "", "", "", now.Add(6*time.Minute)); err == nil {
		t.Fatal("stale proof must be rejected")
	}
}
