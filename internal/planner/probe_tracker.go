package planner

import "sync"

type probeState struct {
	Eligible             bool
	ConsecutiveFailures  int
	ConsecutiveSuccesses int
}

type ProbeTracker struct {
	mu               sync.Mutex
	states           map[string]probeState
	failureThreshold int
	successThreshold int
}

func NewProbeTracker(failureThreshold, successThreshold int) *ProbeTracker {
	return &ProbeTracker{
		states:           make(map[string]probeState),
		failureThreshold: failureThreshold,
		successThreshold: successThreshold,
	}
}

func (tracker *ProbeTracker) Update(name string, apiReady, probeSucceeded bool) bool {
	tracker.mu.Lock()
	defer tracker.mu.Unlock()

	state := tracker.states[name]
	if !apiReady {
		tracker.states[name] = probeState{}
		return false
	}

	if probeSucceeded {
		state.ConsecutiveFailures = 0

		state.ConsecutiveSuccesses++
		if state.ConsecutiveSuccesses >= tracker.successThreshold {
			state.Eligible = true
		}
	} else {
		state.ConsecutiveSuccesses = 0

		state.ConsecutiveFailures++
		if state.ConsecutiveFailures >= tracker.failureThreshold {
			state.Eligible = false
		}
	}

	tracker.states[name] = state

	return state.Eligible
}

func (tracker *ProbeTracker) Retain(names map[string]struct{}) {
	tracker.mu.Lock()
	defer tracker.mu.Unlock()

	for name := range tracker.states {
		if _, ok := names[name]; !ok {
			delete(tracker.states, name)
		}
	}
}
