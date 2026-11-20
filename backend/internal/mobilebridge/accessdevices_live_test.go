package mobilebridge

import (
	"path/filepath"
	"testing"
	"time"
)

func TestRemovalWaitsForAWriteWhileLive(t *testing.T) {
	for name, remove := range map[string]func(*AccessDevices, AccessDevice){
		"Revoke":    func(r *AccessDevices, d AccessDevice) { _, _, _ = r.Revoke(d.ID) },
		"RevokeAll": func(r *AccessDevices, _ AccessDevice) { _ = r.RevokeAll() },
	} {
		t.Run(name, func(t *testing.T) {
			r, err := LoadAccessDevices(filepath.Join(t.TempDir(), "devices.json"))
			if err != nil {
				t.Fatal(err)
			}
			dev, _, err := r.IssueScoped("Decision sync from laptop", "darwin", []string{DeviceScopeDecisionSync}, time.Now())
			if err != nil {
				t.Fatal(err)
			}
			entered, release, done := make(chan struct{}), make(chan struct{}), make(chan bool, 1)
			go func() {
				ok, _ := r.WhileLive(dev.ID, func() error { close(entered); <-release; return nil })
				done <- ok
			}()
			<-entered
			removed := make(chan struct{})
			go func() { remove(r, dev); close(removed) }()
			select {
			case <-removed:
				t.Fatal("the device was removed while a write it admitted was running")
			case <-time.After(100 * time.Millisecond):
			}
			close(release)
			if !<-done {
				t.Fatal("the admitted write was reported as refused")
			}
			<-removed
			called := false
			if ok, _ := r.WhileLive(dev.ID, func() error { called = true; return nil }); ok || called {
				t.Fatal("a write ran after the device was removed")
			}
		})
	}
}
