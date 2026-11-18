package persistenthost

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/datadirlock"
	"github.com/aoagents/agent-orchestrator/backend/internal/processalive"
)

const (
	hostOwnershipChoosingPrefix = "choosing."
	hostOwnershipTicketPrefix   = "ticket."
	hostOwnershipRecordSuffix   = ".json"
	hostOwnershipQueuePoll      = 25 * time.Millisecond
)

type hostOwnershipMutationLock interface {
	Release() error
}

type hostAdvisoryLockAcquire func(context.Context, string, time.Duration) (*datadirlock.Lock, error)

type hostOwnershipRecord struct {
	Token  string `json:"token"`
	PID    int    `json:"pid"`
	BootID string `json:"bootId,omitempty"`
	Ticket uint64 `json:"ticket"`
}

type hostOwnershipQueueLock struct {
	path string
	once sync.Once
	err  error
}

func (l *hostOwnershipQueueLock) Release() error {
	if l == nil {
		return nil
	}
	l.once.Do(func() {
		if err := os.Remove(l.path); err != nil && !errors.Is(err, os.ErrNotExist) {
			l.err = fmt.Errorf("release chat host ownership queue: %w", err)
		}
	})
	return l.err
}

func acquireHostOwnershipMutationLock(dataDir, sessionID string) (hostOwnershipMutationLock, error) {
	return acquireHostOwnershipMutationLockWith(dataDir, sessionID, datadirlock.Acquire)
}

func acquireHostOwnershipMutationLockWith(
	dataDir, sessionID string,
	acquireAdvisory hostAdvisoryLockAcquire,
) (hostOwnershipMutationLock, error) {
	dir, err := hostDir(dataDir, sessionID)
	if err != nil {
		return nil, err
	}
	lockDir := filepath.Join(dir, ".ownership-lock")
	lock, err := acquireAdvisory(context.Background(), lockDir, startupTimeout)
	if err == nil {
		return lock, nil
	}
	if !errors.Is(err, datadirlock.ErrUnsupported) {
		return nil, fmt.Errorf("%w: lock chat host ownership: %w", ErrOwnershipInconclusive, err)
	}
	queueLock, queueErr := acquireHostOwnershipQueueLock(
		context.Background(), filepath.Join(lockDir, "queue"), startupTimeout,
	)
	if queueErr != nil {
		return nil, fmt.Errorf("%w: lock chat host ownership without advisory locks: %w", ErrOwnershipInconclusive, queueErr)
	}
	return queueLock, nil
}

func acquireHostOwnershipQueueLock(
	ctx context.Context,
	dir string,
	wait time.Duration,
) (_ *hostOwnershipQueueLock, retErr error) {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, err
	}
	if err := os.Chmod(dir, 0o700); err != nil { //nolint:gosec // Owner-only coordination directory.
		return nil, err
	}
	bootID, err := currentBootIdentity()
	if err != nil {
		return nil, fmt.Errorf("read system boot identity: %w", err)
	}
	token, err := randomToken()
	if err != nil {
		return nil, err
	}
	record := hostOwnershipRecord{Token: token, PID: os.Getpid(), BootID: bootID}
	choosingPath := hostOwnershipRecordPath(dir, hostOwnershipChoosingPrefix, token)
	if err := writeHostOwnershipRecord(choosingPath, record); err != nil {
		return nil, err
	}
	defer func() {
		if retErr != nil {
			_ = os.Remove(choosingPath)
		}
	}()

	maxTicket, err := maxActiveHostOwnershipTicket(dir, bootID)
	if err != nil {
		return nil, err
	}
	if maxTicket == math.MaxUint64 {
		return nil, errors.New("chat host ownership ticket space exhausted")
	}
	record.Ticket = maxTicket + 1
	ticketPath := hostOwnershipRecordPath(dir, hostOwnershipTicketPrefix, token)
	if err := writeHostOwnershipRecord(ticketPath, record); err != nil {
		return nil, err
	}
	queueLock := &hostOwnershipQueueLock{path: ticketPath}
	defer func() {
		if retErr != nil {
			_ = queueLock.Release()
		}
	}()
	if err := os.Remove(choosingPath); err != nil {
		return nil, fmt.Errorf("finish chat host ownership ticket selection: %w", err)
	}

	deadline := time.Now().Add(wait)
	for {
		blocked, err := hostOwnershipQueueBlocked(dir, record)
		if err != nil {
			return nil, err
		}
		if !blocked {
			return queueLock, nil
		}
		if wait <= 0 || !time.Now().Before(deadline) {
			return nil, errors.New("timed out waiting for chat host ownership queue")
		}
		timer := time.NewTimer(hostOwnershipQueuePoll)
		select {
		case <-ctx.Done():
			timer.Stop()
			return nil, ctx.Err()
		case <-timer.C:
		}
	}
}

func maxActiveHostOwnershipTicket(dir, currentBootID string) (uint64, error) {
	records, err := readActiveHostOwnershipRecords(dir, hostOwnershipTicketPrefix, currentBootID)
	if err != nil {
		return 0, err
	}
	var maxTicket uint64
	for _, record := range records {
		if record.Ticket > maxTicket {
			maxTicket = record.Ticket
		}
	}
	return maxTicket, nil
}

func hostOwnershipQueueBlocked(dir string, own hostOwnershipRecord) (bool, error) {
	choosing, err := readActiveHostOwnershipRecords(dir, hostOwnershipChoosingPrefix, own.BootID)
	if err != nil {
		return false, err
	}
	for _, record := range choosing {
		if record.Token != own.Token {
			return true, nil
		}
	}
	tickets, err := readActiveHostOwnershipRecords(dir, hostOwnershipTicketPrefix, own.BootID)
	if err != nil {
		return false, err
	}
	foundOwn := false
	for _, record := range tickets {
		if record.Token == own.Token {
			foundOwn = true
			continue
		}
		if record.Ticket < own.Ticket || (record.Ticket == own.Ticket && record.Token < own.Token) {
			return true, nil
		}
	}
	if !foundOwn {
		return false, errors.New("chat host ownership ticket disappeared")
	}
	return false, nil
}

func readActiveHostOwnershipRecords(
	dir, prefix, currentBootID string,
) ([]hostOwnershipRecord, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	records := make([]hostOwnershipRecord, 0, len(entries))
	for _, entry := range entries {
		name := entry.Name()
		if !strings.HasPrefix(name, prefix) || !strings.HasSuffix(name, hostOwnershipRecordSuffix) {
			continue
		}
		token := strings.TrimSuffix(strings.TrimPrefix(name, prefix), hostOwnershipRecordSuffix)
		if !validLaunchToken(token) {
			return nil, fmt.Errorf("invalid chat host ownership record %q", name)
		}
		path := filepath.Join(dir, name)
		var record hostOwnershipRecord
		if err := readLaunchJSON(path, &record); err != nil {
			if errors.Is(err, os.ErrNotExist) {
				continue
			}
			return nil, fmt.Errorf("read chat host ownership record %q: %w", name, err)
		}
		if record.Token != token || record.PID <= 0 {
			return nil, fmt.Errorf("invalid chat host ownership record %q", name)
		}
		if priorBootIdentity(record.BootID, currentBootID) || processalive.Exited(record.PID) {
			_ = os.Remove(path)
			continue
		}
		records = append(records, record)
	}
	return records, nil
}

func hostOwnershipRecordPath(dir, prefix, token string) string {
	return filepath.Join(dir, prefix+token+hostOwnershipRecordSuffix)
}

func writeHostOwnershipRecord(path string, record hostOwnershipRecord) error {
	b, err := json.Marshal(record)
	if err != nil {
		return err
	}
	return writeAtomicLaunchFile(path, b)
}
