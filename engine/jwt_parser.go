// SPDX-License-Identifier: GPL-3.0-or-later

// Copyright (c) 2025 Spruce Health

package engine

import (
	"fmt"

	"github.com/twilio/twilio-go/client/jwt"
)

// ParseJWTForApplicationSID parses a JWT token and extracts the application SID from the voice grant
// tokenString is the JWT token to parse
// secret is the signing secret used to validate the token. Pass empty string to skip validation.
// Returns the application SID from the voice grant's outgoing configuration
func ParseJWTForApplicationSID(tokenString string, secret string) (string, error) {
	// Create an AccessToken instance
	accessToken := &jwt.AccessToken{}

	// Parse the JWT token
	// Pass empty string as key to skip signature validation, or pass the actual secret to validate
	decodedToken, err := accessToken.FromJwt(tokenString, secret)
	if err != nil {
		return "", fmt.Errorf("failed to parse JWT: %w", err)
	}

	// Look through the grants to find the VoiceGrant
	for _, grant := range decodedToken.Grants {
		if voiceGrant, ok := grant.(*jwt.VoiceGrant); ok {
			// Extract the application SID from the outgoing configuration
			if voiceGrant.Outgoing.ApplicationSid != "" {
				return voiceGrant.Outgoing.ApplicationSid, nil
			}
		}
	}

	return "", fmt.Errorf("no voice grant with application SID found in token")
}

// ParseJWTToken parses a JWT token and returns the decoded AccessToken
// This gives you access to all claims in the token including account SID, identity, grants, etc.
// tokenString is the JWT token to parse
// secret is the signing secret used to validate the token. Pass empty string to skip validation.
func ParseJWTToken(tokenString string, secret string) (*jwt.AccessToken, error) {
	accessToken := &jwt.AccessToken{}
	decodedToken, err := accessToken.FromJwt(tokenString, secret)
	if err != nil {
		return nil, fmt.Errorf("failed to parse JWT: %w", err)
	}
	return decodedToken, nil
}

// GetVoiceGrantFromToken extracts the VoiceGrant from a decoded AccessToken
func GetVoiceGrantFromToken(token *jwt.AccessToken) (*jwt.VoiceGrant, error) {
	for _, grant := range token.Grants {
		if voiceGrant, ok := grant.(*jwt.VoiceGrant); ok {
			return voiceGrant, nil
		}
	}
	return nil, fmt.Errorf("no voice grant found in token")
}
