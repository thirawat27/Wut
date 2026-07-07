// Package metrics provides metrics collection for WUT
package metrics

import (
	"context"
	"net/http"
	"runtime"
	"sync"
	"sync/atomic"
	"time"

	"github.com/goccy/go-json"
)

// Metrics holds all application metrics
type Metrics struct {
	// Command metrics
	CommandsSuggested   atomic.Int64
	CommandsExecuted    atomic.Int64
	CommandsExplained   atomic.Int64
	CommandsHistoryView atomic.Int64

	// Performance metrics
	RequestCount      atomic.Int64
	RequestErrors     atomic.Int64
	RequestDuration   atomic.Int64 // milliseconds
	ActiveConnections atomic.Int64

	// System metrics
	StartTime time.Time
	Version   string
	Commit    string

	// Custom metrics
	customCounters   map[string]*atomic.Int64
	customGauges     map[string]*atomic.Int64
	customHistograms map[string]*histogram
	mu               sync.RWMutex
}

// histogram represents a histogram metric
type histogram struct {
	buckets []int64
	counts  []atomic.Int64
	sum     atomic.Int64
	count   atomic.Int64
}

var (
	// globalMetrics is the global metrics instance
	globalMetrics *Metrics
	// once ensures metrics is initialized only once
	once sync.Once
)

// Initialize initializes the global metrics
func Initialize(version, commit string) *Metrics {
	once.Do(func() {
		globalMetrics = &Metrics{
			StartTime:        time.Now(),
			Version:          version,
			Commit:           commit,
			customCounters:   make(map[string]*atomic.Int64),
			customGauges:     make(map[string]*atomic.Int64),
			customHistograms: make(map[string]*histogram),
		}
	})
	return globalMetrics
}

// Get returns the global metrics instance
func Get() *Metrics {
	if globalMetrics == nil {
		return Initialize("1.0.1", "unknown")
	}
	return globalMetrics
}

// RecordCommandSuggested increments the commands suggested counter
func (m *Metrics) RecordCommandSuggested() {
	m.CommandsSuggested.Add(1)
}

// RecordCommandExecuted increments the commands executed counter
func (m *Metrics) RecordCommandExecuted() {
	m.CommandsExecuted.Add(1)
}

// RecordCommandExplained increments the commands explained counter
func (m *Metrics) RecordCommandExplained() {
	m.CommandsExplained.Add(1)
}

// RecordHistoryView increments the history view counter
func (m *Metrics) RecordHistoryView() {
	m.CommandsHistoryView.Add(1)
}

// RecordRequest records a request
func (m *Metrics) RecordRequest(duration time.Duration, err error) {
	m.RequestCount.Add(1)
	m.RequestDuration.Add(int64(duration.Milliseconds()))
	if err != nil {
		m.RequestErrors.Add(1)
	}
}

// IncrementActiveConnections increments active connections
func (m *Metrics) IncrementActiveConnections() {
	m.ActiveConnections.Add(1)
}

// DecrementActiveConnections decrements active connections
func (m *Metrics) DecrementActiveConnections() {
	for {
		current := m.ActiveConnections.Load()
		if current <= 0 {
			return
		}
		if m.ActiveConnections.CompareAndSwap(current, current-1) {
			return
		}
	}
}

// IncrementCounter increments a custom counter
func (m *Metrics) IncrementCounter(name string) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if counter, ok := m.customCounters[name]; ok {
		counter.Add(1)
	} else {
		newCounter := &atomic.Int64{}
		newCounter.Add(1)
		m.customCounters[name] = newCounter
	}
}

// SetGauge sets a custom gauge value
func (m *Metrics) SetGauge(name string, value int64) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if gauge, ok := m.customGauges[name]; ok {
		gauge.Store(value)
	} else {
		newGauge := &atomic.Int64{}
		newGauge.Store(value)
		m.customGauges[name] = newGauge
	}
}

// RecordHistogram records a value in a histogram
func (m *Metrics) RecordHistogram(name string, value int64, buckets []int64) {
	m.mu.Lock()
	h, ok := m.customHistograms[name]
	if !ok {
		h = &histogram{
			buckets: buckets,
			counts:  make([]atomic.Int64, len(buckets)+1),
		}
		m.customHistograms[name] = h
	}
	m.mu.Unlock()

	h.sum.Add(value)
	h.count.Add(1)

	// Find bucket
	for i, bucket := range buckets {
		if value <= bucket {
			h.counts[i].Add(1)
			return
		}
	}
	// Value exceeds all buckets
	h.counts[len(buckets)].Add(1)
}

// GetUptime returns application uptime
func (m *Metrics) GetUptime() time.Duration {
	return time.Since(m.StartTime)
}

// Snapshot returns a snapshot of current metrics
func (m *Metrics) Snapshot() map[string]any {
	var memStats runtime.MemStats
	runtime.ReadMemStats(&memStats)

	m.mu.RLock()
	customCounters := make(map[string]int64)
	for name, counter := range m.customCounters {
		customCounters[name] = counter.Load()
	}

	customGauges := make(map[string]int64)
	for name, gauge := range m.customGauges {
		customGauges[name] = gauge.Load()
	}
	m.mu.RUnlock()

	return map[string]any{
		"commands": map[string]int64{
			"suggested":    m.CommandsSuggested.Load(),
			"executed":     m.CommandsExecuted.Load(),
			"explained":    m.CommandsExplained.Load(),
			"history_view": m.CommandsHistoryView.Load(),
		},
		"performance": map[string]any{
			"requests":           m.RequestCount.Load(),
			"errors":             m.RequestErrors.Load(),
			"avg_request_time":   m.getAvgRequestTime(),
			"active_connections": m.ActiveConnections.Load(),
		},
		"system": map[string]any{
			"uptime":     m.GetUptime().String(),
			"version":    m.Version,
			"commit":     m.Commit,
			"goroutines": runtime.NumGoroutine(),
			"memory": map[string]any{
				"alloc":       memStats.Alloc,
				"total_alloc": memStats.TotalAlloc,
				"sys":         memStats.Sys,
				"num_gc":      memStats.NumGC,
			},
		},
		"custom_counters": customCounters,
		"custom_gauges":   customGauges,
	}
}

// getAvgRequestTime returns average request time
func (m *Metrics) getAvgRequestTime() float64 {
	count := m.RequestCount.Load()
	if count == 0 {
		return 0
	}
	return float64(m.RequestDuration.Load()) / float64(count)
}

// JSON returns metrics as JSON
func (m *Metrics) JSON() ([]byte, error) {
	return json.MarshalIndent(m.Snapshot(), "", "  ")
}

// StartServer starts the metrics HTTP server
func (m *Metrics) StartServer(ctx context.Context, addr string) error {
	mux := http.NewServeMux()
	mux.HandleFunc("/metrics", m.handleMetrics)
	mux.HandleFunc("/health", m.handleHealth)

	server := &http.Server{
		Addr:    addr,
		Handler: mux,
	}

	go func() {
		<-ctx.Done()
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = server.Shutdown(shutdownCtx)
	}()

	return server.ListenAndServe()
}

// handleMetrics handles /metrics endpoint
func (m *Metrics) handleMetrics(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	data, err := m.JSON()
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	_, _ = w.Write(data)
}

// handleHealth handles /health endpoint
func (m *Metrics) handleHealth(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	response := map[string]any{
		"status":    "healthy",
		"uptime":    m.GetUptime().String(),
		"version":   m.Version,
		"timestamp": time.Now().UTC().Format(time.RFC3339),
	}
	_ = json.NewEncoder(w).Encode(response)
}

// Convenience functions

// RecordCommandSuggested increments the global commands suggested counter
func RecordCommandSuggested() {
	Get().RecordCommandSuggested()
}

// RecordCommandExecuted increments the global commands executed counter
func RecordCommandExecuted() {
	Get().RecordCommandExecuted()
}

// RecordCommandExplained increments the global commands explained counter
func RecordCommandExplained() {
	Get().RecordCommandExplained()
}

// RecordHistoryView increments the global history view counter
func RecordHistoryView() {
	Get().RecordHistoryView()
}

// RecordRequest records a global request
func RecordRequest(duration time.Duration, err error) {
	Get().RecordRequest(duration, err)
}
