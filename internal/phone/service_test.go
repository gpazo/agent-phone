package phone

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"
)

func TestExclusiveLeaseAcrossClients(t *testing.T) {
	s := &Service{data: t.TempDir(), phones: map[string]*Phone{"device": {UDID: "device", Name: "test"}}}
	start := make(chan struct{})
	var wg sync.WaitGroup
	var mu sync.Mutex
	wins := 0
	busy := 0
	for _, owner := range []string{"agent-a", "agent-b"} {
		wg.Add(1)
		go func(owner string) {
			defer wg.Done()
			<-start
			_, err := s.Call(context.Background(), Request{"acquire", Params{Device: "device", Owner: owner}})
			mu.Lock()
			defer mu.Unlock()
			if err == nil {
				wins++
			} else {
				var api *APIError
				if errors.As(err, &api) && api.Code == "device_busy" {
					busy++
				} else {
					t.Errorf("unexpected error: %v", err)
				}
			}
		}(owner)
	}
	close(start)
	wg.Wait()
	if wins != 1 || busy != 1 {
		t.Fatalf("wins=%d busy=%d", wins, busy)
	}
}
func TestLeaseRejectsWrongAndExpiredTokens(t *testing.T) {
	now := time.Now()
	lease := &Lease{Token: "secret", Owner: "agent", Expires: now.Add(time.Minute)}
	for _, token := range []string{"", "different"} {
		if validateLease(lease, token, now) == nil {
			t.Fatal("accepted wrong token")
		}
	}
	if validateLease(lease, "secret", now.Add(2*time.Minute)) == nil {
		t.Fatal("accepted expired token")
	}
	if err := validateLease(lease, "secret", now); err != nil {
		t.Fatal(err)
	}
}
func TestUnmanagedDeviceCannotBeAcquiredOrMutated(t *testing.T) {
	s := &Service{phones: map[string]*Phone{}}
	for _, method := range []string{"acquire", "shutdown", "boot", "install", "act"} {
		_, err := s.Call(context.Background(), Request{method, Params{Device: "users-personal-phone", Owner: "test"}})
		var api *APIError
		if !errors.As(err, &api) || api.Code != "unmanaged_device" {
			t.Fatalf("%s: %v", method, err)
		}
	}
}
func TestBudgetCountsAlreadyRunningPhones(t *testing.T) {
	const mib = int64(1024 * 1024)
	if budgetFits(3100*mib, 1200*mib, 4096*mib) {
		t.Fatal("admitted boot that exceeds budget")
	}
	if !budgetFits(2700*mib, 1200*mib, 4096*mib) {
		t.Fatal("rejected fitting boot")
	}
	if budgetFits(-1, 1, 4096*mib) {
		t.Fatal("unknown usage treated as free memory")
	}
}
func TestBuildIsolation(t *testing.T) {
	a := Params{Project: "/one/App.xcodeproj", Scheme: "App", Configuration: "Debug"}
	b := a
	b.Project = "/two/App.xcodeproj"
	if buildKey(a) == buildKey(b) {
		t.Fatal("projects share build directory")
	}
	b = a
	b.BuildSettings = map[string]string{"SWIFT_ACTIVE_COMPILATION_CONDITIONS": "FEATURE_A"}
	if buildKey(a) == buildKey(b) {
		t.Fatal("feature variants share build key")
	}
}
