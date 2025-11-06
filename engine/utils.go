// SPDX-License-Identifier: GPL-3.0-or-later

// Copyright (c) 2025 Spruce Health

package engine

import (
	"errors"
	"fmt"
	"net/url"

	"github.com/sprucehealth/twimulator/model"
)

var ErrNotFound = errors.New("not found")

func (s *StateSnapshot) QueueForCall(callSID model.SID) (*model.Queue, error) {
	for _, queue := range s.Queues {
		for _, member := range queue.Members {
			if member == callSID {
				return queue, nil
			}
		}
	}
	return nil, ErrNotFound
}

// resolveActionURL resolves URL relative to the current TwiML document URL
func resolveURL(currentDocURL, actionURL string) (string, error) {
	target, err := url.Parse(actionURL)
	if err != nil {
		return "", fmt.Errorf("invalid action URL %q: %w", actionURL, err)
	}

	if target.IsAbs() {
		return target.String(), nil
	}

	if currentDocURL == "" {
		return "", fmt.Errorf("cannot resolve relative action URL %q without base", actionURL)
	}

	base, err := url.Parse(currentDocURL)
	if err != nil {
		return "", fmt.Errorf("invalid base URL %q: %w", currentDocURL, err)
	}

	return base.ResolveReference(target).String(), nil
}
