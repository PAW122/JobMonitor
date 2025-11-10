package server

import (
	"fmt"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/gorilla/websocket"
	"jobmonitor/internal/cluster"
	"jobmonitor/internal/models"
)

const (
	overviewBucketMinutes   = 10
	overviewBucketCount     = 3
	overviewBucketSeconds   = overviewBucketMinutes * 60
	overviewPushInterval    = 60 * time.Second
	overviewWriteTimeout    = 5 * time.Second
	overviewStateUnknown    = "unknown"
	overviewStateOK         = "ok"
	overviewStateIssue      = "issue"
	overviewConnectivityID  = "connectivity"
	overviewConnectivityKey = "connectivity"
)

var overviewUpgrader = websocket.Upgrader{
	CheckOrigin: func(r *http.Request) bool {
		origin := r.Header.Get("Origin")
		if origin == "" {
			return true
		}
		u, err := url.Parse(origin)
		if err != nil {
			return false
		}
		host := strings.ToLower(strings.TrimSpace(r.Host))
		originHost := strings.ToLower(strings.TrimSpace(u.Host))
		return host == originHost
	},
}

type overviewSnapshot struct {
	GeneratedAt   time.Time       `json:"generated_at"`
	RangeStart    time.Time       `json:"range_start"`
	RangeEnd      time.Time       `json:"range_end"`
	BucketSeconds int             `json:"bucket_seconds"`
	Items         []overviewItem  `json:"items"`
	Node          cluster.Node    `json:"node"`
	Targets       []models.Target `json:"targets"`
}

type overviewItem struct {
	ID      string           `json:"id"`
	Name    string           `json:"name"`
	Kind    string           `json:"kind"`
	Buckets []overviewBucket `json:"buckets"`
}

type overviewBucket struct {
	Start  time.Time `json:"start"`
	End    time.Time `json:"end"`
	State  string    `json:"state"`
	Detail string    `json:"detail,omitempty"`
}

type timeBucket struct {
	Start time.Time
	End   time.Time
}

func (s *Server) handleOverview(w http.ResponseWriter, r *http.Request) {
	limit := parseOverviewLimit(r)
	writeJSON(w, http.StatusOK, s.buildOverviewSnapshot(limit))
}

func (s *Server) handleOverviewWS(w http.ResponseWriter, r *http.Request) {
	limit := parseOverviewLimit(r)
	conn, err := overviewUpgrader.Upgrade(w, r, nil)
	if err != nil {
		return
	}
	s.serveOverviewConnection(conn, limit)
}

func (s *Server) serveOverviewConnection(conn *websocket.Conn, limit int) {
	defer conn.Close()

	if err := writeOverviewPayload(conn, s.buildOverviewSnapshot(limit)); err != nil {
		return
	}

	ticker := time.NewTicker(overviewPushInterval)
	defer ticker.Stop()

	done := make(chan struct{})
	go func() {
		defer close(done)
		for {
			if _, _, err := conn.ReadMessage(); err != nil {
				return
			}
		}
	}()

	for {
		select {
		case <-ticker.C:
			if err := writeOverviewPayload(conn, s.buildOverviewSnapshot(limit)); err != nil {
				return
			}
		case <-done:
			return
		}
	}
}

func writeOverviewPayload(conn *websocket.Conn, payload overviewSnapshot) error {
	_ = conn.SetWriteDeadline(time.Now().Add(overviewWriteTimeout))
	return conn.WriteJSON(payload)
}

func (s *Server) buildOverviewSnapshot(limit int) overviewSnapshot {
	now := time.Now().UTC()
	bucketDuration := time.Duration(overviewBucketMinutes) * time.Minute
	rangeStart := now.Add(-bucketDuration * overviewBucketCount)
	buckets := buildTimeBuckets(rangeStart, bucketDuration, overviewBucketCount)

	history := s.storage.HistorySince(rangeStart)
	connectivityHistory := s.connectivityHistory(rangeStart, now)
	serviceNames := gatherServiceNames(s.targets, history)

	serviceBuckets := make(map[string][]overviewBucket, len(serviceNames))
	for id := range serviceNames {
		serviceBuckets[id] = newOverviewBuckets(buckets)
	}

	for _, entry := range history {
		ts := entry.Timestamp.UTC()
		if ts.Before(rangeStart) || ts.After(now) {
			continue
		}
		idx := bucketIndex(ts, buckets)
		if idx == -1 {
			continue
		}
		for _, check := range entry.Checks {
			id := strings.TrimSpace(check.ID)
			if id == "" {
				continue
			}
			if _, ok := serviceBuckets[id]; !ok {
				serviceNames[id] = serviceDisplayName(id, check.Name)
				serviceBuckets[id] = newOverviewBuckets(buckets)
			}
			updateServiceBucket(&serviceBuckets[id][idx], check)
		}
	}

	serviceIDs := make([]string, 0, len(serviceBuckets))
	for id := range serviceBuckets {
		serviceIDs = append(serviceIDs, id)
	}
	sortServiceIDs(serviceIDs, serviceNames, s.targets)
	if limit > 0 && len(serviceIDs) > limit {
		serviceIDs = serviceIDs[:limit]
	}

	items := make([]overviewItem, 0, len(serviceIDs)+1)
	items = append(items, overviewItem{
		ID:      overviewConnectivityID,
		Name:    "Connectivity",
		Kind:    overviewConnectivityKey,
		Buckets: buildConnectivityBuckets(buckets, connectivityHistory),
	})
	for _, id := range serviceIDs {
		items = append(items, overviewItem{
			ID:      id,
			Name:    serviceNames[id],
			Kind:    "service",
			Buckets: serviceBuckets[id],
		})
	}

	return overviewSnapshot{
		GeneratedAt:   now,
		RangeStart:    rangeStart,
		RangeEnd:      now,
		BucketSeconds: overviewBucketSeconds,
		Items:         items,
		Node:          s.node,
		Targets:       s.targets,
	}
}

func parseOverviewLimit(r *http.Request) int {
	raw := strings.TrimSpace(r.URL.Query().Get("limit"))
	if raw == "" {
		return 0
	}
	value, err := strconv.Atoi(raw)
	if err != nil || value <= 0 {
		return 0
	}
	return value
}

func buildTimeBuckets(start time.Time, duration time.Duration, count int) []timeBucket {
	result := make([]timeBucket, 0, count)
	current := start
	for i := 0; i < count; i++ {
		end := current.Add(duration)
		result = append(result, timeBucket{Start: current, End: end})
		current = end
	}
	return result
}

func newOverviewBuckets(buckets []timeBucket) []overviewBucket {
	result := make([]overviewBucket, len(buckets))
	for i, bucket := range buckets {
		result[i] = overviewBucket{
			Start: bucket.Start,
			End:   bucket.End,
			State: overviewStateUnknown,
		}
	}
	return result
}

func bucketIndex(ts time.Time, buckets []timeBucket) int {
	if len(buckets) == 0 {
		return -1
	}
	if ts.Before(buckets[0].Start) || ts.After(buckets[len(buckets)-1].End) {
		if ts.Equal(buckets[len(buckets)-1].End) {
			return len(buckets) - 1
		}
		return -1
	}
	for i, bucket := range buckets {
		if (ts.Equal(bucket.Start) || ts.After(bucket.Start)) && ts.Before(bucket.End) {
			return i
		}
	}
	if ts.Equal(buckets[len(buckets)-1].End) {
		return len(buckets) - 1
	}
	return -1
}

func gatherServiceNames(targets []models.Target, history []models.StatusEntry) map[string]string {
	names := make(map[string]string)
	for _, target := range targets {
		id := strings.TrimSpace(target.ID)
		if id == "" {
			continue
		}
		names[id] = serviceDisplayName(id, target.Name)
	}
	for _, entry := range history {
		for _, check := range entry.Checks {
			id := strings.TrimSpace(check.ID)
			if id == "" {
				continue
			}
			if _, exists := names[id]; !exists || names[id] == "" {
				names[id] = serviceDisplayName(id, check.Name)
			}
		}
	}
	return names
}

func sortServiceIDs(ids []string, names map[string]string, targets []models.Target) {
	order := make(map[string]int, len(targets))
	for idx, target := range targets {
		if target.ID == "" {
			continue
		}
		order[target.ID] = idx
	}
	sort.Slice(ids, func(i, j int) bool {
		idA := ids[i]
		idB := ids[j]
		idxA, okA := order[idA]
		idxB, okB := order[idB]
		switch {
		case okA && okB && idxA != idxB:
			return idxA < idxB
		case okA:
			return true
		case okB:
			return false
		}
		nameA := strings.ToLower(names[idA])
		nameB := strings.ToLower(names[idB])
		if nameA == nameB {
			return idA < idB
		}
		return nameA < nameB
	})
}

func serviceDisplayName(id, name string) string {
	name = strings.TrimSpace(name)
	if name != "" {
		return name
	}
	return strings.TrimSpace(id)
}

func updateServiceBucket(bucket *overviewBucket, check models.CheckResult) {
	state := strings.TrimSpace(check.State)
	if strings.EqualFold(state, "active") || (state == "" && check.OK) {
		setBucketOK(bucket, "")
		return
	}
	detail := state
	if detail == "" && check.Error != nil {
		detail = strings.TrimSpace(*check.Error)
	}
	if detail == "" {
		detail = "inactive"
	}
	setBucketIssue(bucket, detail)
}

func buildConnectivityBuckets(buckets []timeBucket, history []models.ConnectivityStatus) []overviewBucket {
	result := newOverviewBuckets(buckets)
	if len(history) == 0 {
		return result
	}
	for _, sample := range history {
		idx := bucketIndex(sample.CheckedAt.UTC(), buckets)
		if idx == -1 {
			continue
		}
		if sample.OK {
			detail := ""
			if sample.LatencyMs > 0 {
				detail = fmt.Sprintf("%d ms", sample.LatencyMs)
			}
			setBucketOK(&result[idx], detail)
			continue
		}
		detail := strings.TrimSpace(sample.Error)
		if detail == "" {
			detail = "offline"
		}
		setBucketIssue(&result[idx], detail)
	}
	return result
}

func setBucketOK(bucket *overviewBucket, detail string) {
	if bucket.State == overviewStateIssue {
		return
	}
	if bucket.State == overviewStateUnknown {
		bucket.State = overviewStateOK
	}
	if detail != "" {
		bucket.Detail = detail
	}
}

func setBucketIssue(bucket *overviewBucket, detail string) {
	bucket.State = overviewStateIssue
	if detail != "" {
		bucket.Detail = detail
	}
}
