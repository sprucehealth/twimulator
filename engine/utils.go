package engine

import (
	"errors"
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
