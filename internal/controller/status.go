package controller

import (
	"encoding/json"
	"errors"
	"net/http"
	"slices"
	"sync"
	"time"

	"github.com/zijiren233/route-controller/internal/planner"
	"github.com/zijiren233/route-controller/internal/route"
)

var errNotReady = errors.New("route controller is not ready")

type Status struct {
	StartedAt       time.Time        `json:"startedAt"`
	CacheSynced     bool             `json:"cacheSynced"`
	Ready           bool             `json:"ready"`
	DryRun          bool             `json:"dryRun"`
	LastReconcileAt *time.Time       `json:"lastReconcileAt,omitempty"`
	LastSuccessAt   *time.Time       `json:"lastSuccessAt,omitempty"`
	LastError       string           `json:"lastError,omitempty"`
	DesiredRoutes   []route.Desired  `json:"desiredRoutes,omitempty"`
	LastActions     []route.Action   `json:"lastActions,omitempty"`
	Workers         []planner.Worker `json:"workers,omitempty"`
}

type StatusStore struct {
	mu     sync.RWMutex
	status Status
}

func NewStatusStore(dryRun bool) *StatusStore {
	return &StatusStore{status: Status{StartedAt: time.Now().UTC(), DryRun: dryRun}}
}

func (store *StatusStore) Record(
	ready bool,
	err error,
	desired []route.Desired,
	actions []route.Action,
	workers []planner.Worker,
) {
	store.mu.Lock()
	defer store.mu.Unlock()

	now := time.Now().UTC()
	store.status.CacheSynced = true
	store.status.Ready = ready
	store.status.LastReconcileAt = &now
	store.status.DesiredRoutes = cloneRoutes(desired)
	store.status.LastActions = slices.Clone(actions)

	store.status.Workers = cloneWorkers(workers)
	if err != nil {
		store.status.LastError = err.Error()
		return
	}

	store.status.LastError = ""
	if ready {
		store.status.LastSuccessAt = &now
	}
}

func (store *StatusStore) Snapshot() Status {
	store.mu.RLock()
	defer store.mu.RUnlock()

	snapshot := store.status
	if store.status.LastReconcileAt != nil {
		value := *store.status.LastReconcileAt
		snapshot.LastReconcileAt = &value
	}

	if store.status.LastSuccessAt != nil {
		value := *store.status.LastSuccessAt
		snapshot.LastSuccessAt = &value
	}

	snapshot.DesiredRoutes = cloneRoutes(store.status.DesiredRoutes)
	snapshot.LastActions = slices.Clone(store.status.LastActions)
	snapshot.Workers = cloneWorkers(store.status.Workers)

	return snapshot
}

func (store *StatusStore) Ready(_ *http.Request) error {
	snapshot := store.Snapshot()
	if snapshot.Ready {
		return nil
	}

	if snapshot.LastError != "" {
		return errors.New(snapshot.LastError)
	}

	return errNotReady
}

func (store *StatusStore) ServeHTTP(writer http.ResponseWriter, request *http.Request) {
	if request.Method != http.MethodGet {
		writer.Header().Set("Allow", http.MethodGet)
		http.Error(writer, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	writer.Header().Set("Cache-Control", "no-store")
	writer.Header().Set("Content-Type", "application/json")

	if err := json.NewEncoder(writer).Encode(store.Snapshot()); err != nil {
		http.Error(writer, "encode status", http.StatusInternalServerError)
	}
}

func cloneRoutes(source []route.Desired) []route.Desired {
	cloned := make([]route.Desired, len(source))
	for index := range source {
		cloned[index] = source[index]
		cloned[index].Gateways = slices.Clone(source[index].Gateways)
	}

	return cloned
}

func cloneWorkers(source []planner.Worker) []planner.Worker {
	cloned := make([]planner.Worker, len(source))
	for index := range source {
		cloned[index] = source[index]
		cloned[index].PodCIDRs = slices.Clone(source[index].PodCIDRs)
	}

	return cloned
}
