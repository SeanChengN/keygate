package service

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"strings"
	"time"

	keygatelicense "github.com/tabloy/keygate/internal/license"
	"github.com/tabloy/keygate/internal/store"
)

type DeviceProofInput struct {
	PublicKey string
	Timestamp int64
	Nonce     string
	Signature string
}

func (p DeviceProofInput) provided() bool {
	return strings.TrimSpace(p.PublicKey) != "" || p.Timestamp != 0 ||
		strings.TrimSpace(p.Nonce) != "" || strings.TrimSpace(p.Signature) != ""
}

func validateDeviceProof(
	ctx context.Context,
	s *store.Store,
	proof DeviceProofInput,
	action, licenseKey, identifier, productID, platform, channel, version string,
	expectedPublicKey string,
) error {
	if !proof.provided() || (expectedPublicKey != "" && proof.PublicKey != expectedPublicKey) {
		return licenseNotFound()
	}
	now := time.Now().UTC()
	if err := keygatelicense.VerifyDeviceProof(keygatelicense.DeviceProof{
		PublicKey: proof.PublicKey, Timestamp: proof.Timestamp,
		Nonce: proof.Nonce, Signature: proof.Signature,
	}, action, licenseKey, identifier, productID, platform, channel, version, now); err != nil {
		return licenseNotFound()
	}
	digest := sha256.Sum256([]byte(proof.PublicKey + ":" + proof.Nonce))
	consumed, err := s.ConsumeDeviceProofNonce(ctx, hex.EncodeToString(digest[:]), now.Add(10*time.Minute))
	if err != nil {
		return err
	}
	if !consumed {
		return licenseNotFound()
	}
	return nil
}
