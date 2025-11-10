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

type overviewServiceEntry struct {
	id       string
	name     string
	timeline []models.TimelinePoint
	order    int
	hasOrder bool
}

type overviewNodeGroup struct {
	nodeID   string
	nodeName string
	isLocal  bool
	services []overviewServiceEntry
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

func (s *Server) overviewConnectivityItem(buckets []timeBucket) overviewItem {
	start := time.Time{}
	end := time.Time{}
	if len(buckets) > 0 {
		start = buckets[0].Start
		end = buckets[len(buckets)-1].End
	}
	history := s.connectivityHistory(start, end)
	return overviewItem{
		ID:      overviewConnectivityID,
		Name:    "Connectivity",
		Kind:    overviewConnectivityKey,
		Buckets: buildConnectivityBuckets(buckets, history),
	}
}

func (s *Server) overviewServiceItems(limit int, buckets []timeBucket, start, end time.Time) []overviewItem {
	if len(buckets) == 0 {
		return nil
	}
	snapshot := s.overviewClusterSnapshot(start, end)
	groups := s.buildServiceGroups(snapshot)
	if len(groups) == 0 {
		return nil
	}
	entries := pickServicesRoundRobin(groups, limit)
	items := make([]overviewItem, 0, len(entries))
	for _, entry := range entries {
		items = append(items, overviewItem{
			ID:      entry.id,
			Name:    entry.name,
			Kind:    "service",
			Buckets: mapTimelineToBuckets(entry.timeline, buckets),
		})
	}
	return items
}

func (s *Server) overviewClusterSnapshot(start, end time.Time) cluster.ClusterSnapshot {
	if s.clusterService != nil {
		snapshot := s.clusterService.Snapshot(start, end)
		if len(snapshot.Nodes) > 0 {
			return snapshot
		}
	}
	local := s.localPeerSnapshot(window{
		start: start,
		end:   end,
	})
	return cluster.ClusterSnapshot{
		GeneratedAt: time.Now().UTC(),
		Range:       overviewRangeKey(start, end),
		RangeStart:  start,
		RangeEnd:    end,
		Nodes:       []cluster.PeerSnapshot{local},
	}
}

func (s *Server) buildServiceGroups(snapshot cluster.ClusterSnapshot) []overviewNodeGroup {
	if len(snapshot.Nodes) == 0 {
		return nil
	}
	localOrder := buildTargetOrder(s.targets)
	multiNode := len(snapshot.Nodes) > 1

	groups := make([]overviewNodeGroup, 0, len(snapshot.Nodes))
	for _, nodeSnap := range snapshot.Nodes {
		group := overviewNodeGroup{
			nodeID:   strings.TrimSpace(nodeSnap.Node.ID),
			nodeName: fallbackName(nodeSnap.Node),
			isLocal:  s.isLocalPeer(nodeSnap),
		}
		var order map[string]int
		if group.isLocal {
			order = localOrder
		} else {
			order = buildTargetOrder(nodeSnap.Targets)
		}

		for _, timeline := range nodeSnap.ServiceTimelines {
			if len(timeline.Timeline) == 0 {
				continue
			}
			serviceName := strings.TrimSpace(timeline.ServiceName)
			if serviceName == "" {
				serviceName = strings.TrimSpace(timeline.ServiceID)
			}
			displayName := serviceName
			if multiNode {
				displayName = fmt.Sprintf("%s (%s)", serviceName, group.nodeName)
			}
			entry := overviewServiceEntry{
				id:       fmt.Sprintf("%s::%s", group.nodeID, timeline.ServiceID),
				name:     displayName,
				timeline: timeline.Timeline,
			}
			if idx, ok := order[timeline.ServiceID]; ok {
				entry.order = idx
				entry.hasOrder = true
			}
			group.services = append(group.services, entry)
		}

		sort.SliceStable(group.services, func(i, j int) bool {
			a := group.services[i]
			b := group.services[j]
			switch {
			case a.hasOrder && b.hasOrder && a.order != b.order:
				return a.order < b.order
			case a.hasOrder && !b.hasOrder:
				return true
			case !a.hasOrder && b.hasOrder:
				return false
			default:
				nameA := strings.ToLower(a.name)
				nameB := strings.ToLower(b.name)
				if nameA == nameB {
					return a.id < b.id
				}
				return nameA < nameB
			}
		})

		if len(group.services) > 0 {
			groups = append(groups, group)
		}
	}

	sort.SliceStable(groups, func(i, j int) bool {
		if groups[i].isLocal == groups[j].isLocal {
			nameA := strings.ToLower(groups[i].nodeName)
			nameB := strings.ToLower(groups[j].nodeName)
			if nameA == nameB {
				return groups[i].nodeID < groups[j].nodeID
			}
			return nameA < nameB
		}
		return groups[i].isLocal
	})
	return groups
}

func pickServicesRoundRobin(groups []overviewNodeGroup, limit int) []overviewServiceEntry {
	total := 0
	for _, group := range groups {
		total += len(group.services)
	}
	if total == 0 {
		return nil
	}
	if limit <= 0 || limit > total {
		limit = total
	}
	result := make([]overviewServiceEntry, 0, limit)
	indexes := make([]int, len(groups))
	for len(result) < limit {
		progressed := false
		for i := range groups {
			if indexes[i] >= len(groups[i].services) {
				continue
			}
			result = append(result, groups[i].services[indexes[i]])
			indexes[i]++
			progressed = true
			if len(result) >= limit {
				break
			}
		}
		if !progressed {
			break
		}
	}
	return result
}

func mapTimelineToBuckets(points []models.TimelinePoint, buckets []timeBucket) []overviewBucket {
	result := newOverviewBuckets(buckets)
	if len(points) == 0 {
		return result
	}
	for i, bucket := range buckets {
		state := overviewStateUnknown
		detail := ""
		for _, point := range points {
			if bucketOverlaps(bucket, point.Start, point.End) {
				pointState := timelineState(point.ClassName)
				if pointState == overviewStateIssue {
					state = overviewStateIssue
					detail = timelineDetail(point)
					break
				}
				if pointState == overviewStateOK && state != overviewStateOK {
					state = overviewStateOK
					detail = timelineDetail(point)
				}
			}
		}
		result[i].State = state
		if detail != "" {
			result[i].Detail = detail
		}
	}
	return result
}

func bucketOverlaps(bucket timeBucket, start, end time.Time) bool {
	if start.IsZero() && end.IsZero() {
		return false
	}
	if end.Before(start) {
		end = start
	}
	if bucket.End.Before(bucket.Start) {
		return false
	}
	if end.Equal(bucket.Start) || end.Before(bucket.Start) {
		return false
	}
	if start.Equal(bucket.End) || start.After(bucket.End) {
		return false
	}
	return true
}

func timelineState(className string) string {
	switch strings.ToLower(strings.TrimSpace(className)) {
	case "state-success":
		return overviewStateOK
	case "state-error", "state-warning":
		return overviewStateIssue
	default:
		return overviewStateUnknown
	}
}

func timelineDetail(point models.TimelinePoint) string {
	if len(point.Details) > 0 {
		detail := strings.TrimSpace(point.Details[0].Error)
		if detail == "" {
			detail = strings.TrimSpace(point.Details[0].State)
		}
		if detail != "" {
			return detail
		}
	}
	label := strings.TrimSpace(point.Label)
	if label != "" {
		return label
	}
	return ""
}

func buildTargetOrder(targets []models.Target) map[string]int {
	if len(targets) == 0 {
		return nil
	}
	order := make(map[string]int, len(targets))
	for idx, target := range targets {
		if target.ID == "" {
			continue
		}
		order[target.ID] = idx
	}
	return order
}

func fallbackName(node cluster.Node) string {
	if name := strings.TrimSpace(node.Name); name != "" {
		return name
	}
	return strings.TrimSpace(node.ID)
}

func (s *Server) isLocalPeer(peer cluster.PeerSnapshot) bool {
	if strings.EqualFold(peer.Source, "local") {
		return true
	}
	if peer.Node.ID != "" && s.node.ID != "" {
		return strings.EqualFold(peer.Node.ID, s.node.ID)
	}
	return false
}

func overviewRangeKey(start, end time.Time) string {
	duration := end.Sub(start)
	switch {
	case duration >= 30*24*time.Hour:
		return "30d"
	case duration >= 24*time.Hour:
		return "24h"
	default:
		return ""
	}
}

func (s *Server) buildOverviewSnapshot(limit int) overviewSnapshot {
	now := time.Now().UTC()
	bucketDuration := time.Duration(overviewBucketMinutes) * time.Minute
	rangeStart := now.Add(-bucketDuration * overviewBucketCount)
	buckets := buildTimeBuckets(rangeStart, bucketDuration, overviewBucketCount)

	items := make([]overviewItem, 0, limit+1)
	if len(buckets) > 0 {
		items = append(items, s.overviewConnectivityItem(buckets))
		serviceItems := s.overviewServiceItems(limit, buckets, rangeStart, now)
		items = append(items, serviceItems...)
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
