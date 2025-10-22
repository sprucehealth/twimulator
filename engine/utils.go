package engine

import (
	"errors"
	"fmt"
	"net/url"
	"twimulator/model"
)

var ErrNotFound = errors.New("not found")

func (s *StateSnapshot) QueueForCall(callSID string) (*model.Queue, error) {
	for _, queue := range s.Queues {
		for _, member := range queue.Members {
			if string(member) == callSID {
				return queue, nil
			}
		}
	}
	return nil, ErrNotFound
}

func (s *StateSnapshot) Sync() error {
	if s.accountSID == "" || s.engine == nil {
		return errors.New("SnapshotAll can not call Sync")
	}
	snapshot, err := s.engine.Snapshot(s.accountSID)
	if err != nil {
		return err
	}
	// replace s with the new snapshot
	*s = *snapshot
	return nil
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
