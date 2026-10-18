package systemnotifications

import "sync"

// Authorization coordinates the main window and tray's shared native service.
// Only MainReady starts automatic authorization; explicit settings actions use
// Request. A failed/unknown automatic attempt stays consumed across App restarts.
type Authorization struct {
	Store   *Store
	Status  func() string
	Request func() error

	mu        sync.Mutex
	attempted bool
	flight    *authorizationFlight
}

type authorizationFlight struct {
	done chan struct{}
	err  error
}

func (a *Authorization) MainReady(surface string) error {
	if surface != "main" {
		return nil
	}
	return a.run(true)
}

func (a *Authorization) Authorize() error { return a.run(false) }

func (a *Authorization) run(automatic bool) error {
	a.mu.Lock()
	if flight := a.flight; flight != nil {
		a.mu.Unlock()
		<-flight.done
		return flight.err
	}
	if automatic && a.attempted {
		a.mu.Unlock()
		return nil
	}
	a.attempted = true
	if !a.Store.Enabled() {
		a.mu.Unlock()
		return nil
	}
	flight := &authorizationFlight{done: make(chan struct{})}
	a.flight = flight
	a.mu.Unlock()

	// Persist before reading native state/requesting permission. Even a native
	// status timeout or request error must not cause an automatic retry loop.
	claimed, err := a.Store.ClaimAuthorization()
	// A manual action must work even if persistence is unavailable.
	if !automatic {
		err = nil
	}
	if err == nil && (!automatic || claimed) && a.Status() == "default" {
		err = a.Request()
	}
	a.mu.Lock()
	flight.err = err
	a.flight = nil
	close(flight.done)
	a.mu.Unlock()
	return err
}
