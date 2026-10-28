// SPDX-FileCopyrightText: 2026 Bernd Zeimetz <bernd@bzed.de>
// SPDX-License-Identifier: AGPL-3.0-or-later

package api

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"testing"

	"github.com/fxamacker/cbor/v2"
	"github.com/stretchr/testify/require"
)

// authenticator is a software passkey: it makes the registration and assertion responses a browser would
// send, signed with its own key.
type authenticator struct {
	t       *testing.T
	key     *ecdsa.PrivateKey
	credID  []byte
	counter uint32
	rpID    string
	origin  string
	handle  []byte
}

func newAuthenticator(t *testing.T, rpID, origin string) *authenticator {
	t.Helper()
	k, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	require.NoError(t, err)
	id := make([]byte, 16)
	_, err = rand.Read(id)
	require.NoError(t, err)
	return &authenticator{t: t, key: k, credID: id, rpID: rpID, origin: origin}
}

var b64 = base64.RawURLEncoding

// challenge reads the challenge out of WebAuthn options ({"publicKey":{"challenge":…}}).
func challenge(t *testing.T, options []byte) string {
	t.Helper()
	var o struct {
		PublicKey struct {
			Challenge string `json:"challenge"`
			User      struct {
				ID string `json:"id"`
			} `json:"user"`
		} `json:"publicKey"`
	}
	require.NoError(t, json.Unmarshal(options, &o), string(options))
	require.NotEmpty(t, o.PublicKey.Challenge, string(options))
	return o.PublicKey.Challenge
}

func (a *authenticator) clientData(typ, challenge string) []byte {
	b, err := json.Marshal(map[string]any{"type": typ, "challenge": challenge, "origin": a.origin, "crossOrigin": false})
	require.NoError(a.t, err)
	return b
}

func (a *authenticator) rpHash() []byte { h := sha256.Sum256([]byte(a.rpID)); return h[:] }

// register answers the creation options.
func (a *authenticator) register(options []byte) json.RawMessage {
	a.t.Helper()
	cd := a.clientData("webauthn.create", challenge(a.t, options))
	pub, err := a.key.PublicKey.Bytes() // 0x04 || X || Y
	require.NoError(a.t, err)
	x, y := pub[1:33], pub[33:65]
	cose, err := cbor.Marshal(map[int]any{1: 2, 3: -7, -1: 1, -2: x, -3: y})
	require.NoError(a.t, err)
	auth := append(a.rpHash(), 0x45) // user present, user verified, attested credential data
	auth = binary.BigEndian.AppendUint32(auth, a.counter)
	auth = append(auth, make([]byte, 16)...) // AAGUID
	auth = binary.BigEndian.AppendUint16(auth, uint16(len(a.credID)))
	auth = append(append(auth, a.credID...), cose...)
	att, err := cbor.Marshal(map[string]any{"fmt": "none", "attStmt": map[string]any{}, "authData": auth})
	require.NoError(a.t, err)
	return a.response(map[string]any{"clientDataJSON": b64.EncodeToString(cd), "attestationObject": b64.EncodeToString(att)})
}

// assert answers the request options.
func (a *authenticator) assert(options []byte) json.RawMessage {
	a.t.Helper()
	cd := a.clientData("webauthn.get", challenge(a.t, options))
	a.counter++
	auth := append(a.rpHash(), 0x05) // user present, user verified
	auth = binary.BigEndian.AppendUint32(auth, a.counter)
	h := sha256.Sum256(cd)
	sum := sha256.Sum256(append(append([]byte{}, auth...), h[:]...))
	sig, err := ecdsa.SignASN1(rand.Reader, a.key, sum[:])
	require.NoError(a.t, err)
	return a.response(map[string]any{"clientDataJSON": b64.EncodeToString(cd), "authenticatorData": b64.EncodeToString(auth),
		"signature": b64.EncodeToString(sig), "userHandle": b64.EncodeToString(a.handle)})
}

func (a *authenticator) response(inner map[string]any) json.RawMessage {
	b, err := json.Marshal(map[string]any{"id": b64.EncodeToString(a.credID), "rawId": b64.EncodeToString(a.credID), "type": "public-key", "response": inner})
	require.NoError(a.t, err)
	return b
}
