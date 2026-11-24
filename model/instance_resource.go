package model

import (
	"strconv"
	"strings"
)

// InstanceStatus* describe the coarse state of one API instance. They are the
// only state vocabulary the public summary exposes.
const (
	InstanceStatusHealthy  = "healthy"
	InstanceStatusStale    = "stale"
	InstanceStatusUnknown  = "unknown"
	InstanceStatusDisabled = "disabled"
)

// InstanceMonitoring* is the top-level aggregate monitoring state. It answers
// "is instance monitoring operating at all", not "is a given node healthy".
const (
	InstanceMonitoringEnabled  = "enabled"
	InstanceMonitoringDisabled = "disabled"
	InstanceMonitoringUnknown  = "unknown"
)

// InstanceResourceSummary is the viewer-safe projection of one API instance.
//
// The field set is an allowlist. Every entry of `Domains` and `Key` comes from
// the configured console API settings, which are already published through the
// public status endpoint and are the user-facing API domains. Nothing from the
// stored system-instance record is copied here: no node name, hostname, stored
// address, node URL, sync error, path, process id or OS string can reach this
// struct, and the caller must never populate one.
type InstanceResourceSummary struct {
	// Key is a stable, non-identifying reference derived from the persistent
	// configured API entry id, never from the node name (which often defaults
	// to the hostname) and never from array position, so reordering the
	// entries in settings does not change it.
	Key string `json:"key"`
	// Name is the configured route label of the entry that keys this group.
	Name string `json:"name"`
	// Status is one of InstanceStatus*.
	Status string `json:"status"`
	// Domains are the configured public API URLs bound to this instance, in
	// configuration order. One instance may serve several of them.
	Domains   []string              `json:"domains"`
	Resources InstanceResourceUsage `json:"resources"`
	// ObservedAt is nil when the instance has no usable sample, so the UI can
	// render an explicit unknown state instead of a misleading timestamp.
	ObservedAt *int64 `json:"observed_at"`
	Stale      bool   `json:"stale"`
	Disabled   bool   `json:"disabled"`
}

// InstanceResourceUsage carries the approved CPU/memory/storage percentages. A
// nil pointer means "unknown" so the UI can render an explicit state rather
// than a misleading zero.
type InstanceResourceUsage struct {
	CPUPercent     *float64 `json:"cpu_percent"`
	MemoryPercent  *float64 `json:"memory_percent"`
	StoragePercent *float64 `json:"storage_percent"`
}

// InstanceResourceSummaryResponse is the public response envelope.
type InstanceResourceSummaryResponse struct {
	Instances   []InstanceResourceSummary `json:"instances"`
	GeneratedAt int64                     `json:"generated_at"`
	// Monitoring aggregates the sampling nodes' own monitor state. It is never
	// derived from the reading process's configuration.
	Monitoring string `json:"monitoring"`
}

// ApiInfoBinding is the console API-to-instance binding the summary reads. It
// is configuration data only; no field of it is copied verbatim into the
// summary except the entry id and the configured public URL/route.
type ApiInfoBinding struct {
	ID       int
	NodeName string
	Route    string
	Name     string
}

// BuildInstanceResourceSummary projects stored instances and configured API
// bindings into the safe public summary. One instance (node_name) may bind
// multiple API entries; each bound entry contributes its domain to that
// instance's Domains list. Entries without a node_name binding contribute no
// instance. The emitted Key/Name come from the configured API entry, never the
// raw node_name, so a hostname-defaulted node name never leaks.
//
// The monitoring state is read from each stored record, which the sampling node
// wrote itself; the reading process's own configuration is never consulted, so
// a remote node is never described using local settings.
func BuildInstanceResourceSummary(instances []*SystemInstance, bindings []ApiInfoBinding, now int64) InstanceResourceSummaryResponse {
	bindingsByNode := make(map[string][]ApiInfoBinding, len(bindings))
	nodeOrder := make([]string, 0, len(bindings))
	for _, binding := range bindings {
		node := strings.TrimSpace(binding.NodeName)
		if node == "" {
			continue
		}
		if _, ok := bindingsByNode[node]; !ok {
			nodeOrder = append(nodeOrder, node)
		}
		bindingsByNode[node] = append(bindingsByNode[node], binding)
	}

	instanceByNode := make(map[string]*SystemInstance, len(instances))
	for _, instance := range instances {
		instanceByNode[instance.NodeName] = instance
	}

	summaries := make([]InstanceResourceSummary, 0, len(nodeOrder))
	// Aggregate monitoring: an instance contributes only when the sampling node
	// recorded its own monitor state. Disabled wins over enabled so a partially
	// disabled fleet is never advertised as fully enabled, and an aggregate over
	// nothing stays explicitly unknown.
	enabledNodes := 0
	disabledNodes := 0
	knownNodes := 0
	for _, node := range nodeOrder {
		bound := bindingsByNode[node]
		domains := make([]string, 0, len(bound))
		// The group key uses the smallest persistent entry id bound to this
		// instance, so it stays stable regardless of array order.
		groupID := bound[0].ID
		for _, binding := range bound {
			if binding.Name != "" {
				domains = append(domains, binding.Name)
			}
			if binding.ID < groupID {
				groupID = binding.ID
			}
		}
		summary := InstanceResourceSummary{
			Key:     apiEntryKey(groupID),
			Name:    bound[0].Route,
			Domains: domains,
		}

		instance := instanceByNode[node]
		if instance == nil {
			// No stored record: the instance is not reporting, so nothing is
			// known about it.
			summary.Status = InstanceStatusUnknown
			summaries = append(summaries, summary)
			continue
		}

		observedAt := instance.LastSeenAt
		summary.Stale = now-observedAt > SystemInstanceStaleAfterSeconds
		summary.ObservedAt = &observedAt
		var monitoringDisabled bool
		var monitoringKnown bool
		summary.Resources, monitoringDisabled, monitoringKnown = instanceResourceUsage(instance)
		if monitoringKnown {
			knownNodes++
			if monitoringDisabled {
				disabledNodes++
			} else {
				enabledNodes++
			}
		}
		switch {
		case summary.Stale:
			summary.Status = InstanceStatusStale
		case monitoringDisabled:
			summary.Status = InstanceStatusDisabled
			summary.Disabled = true
		case summary.Resources.CPUPercent != nil ||
			summary.Resources.MemoryPercent != nil ||
			summary.Resources.StoragePercent != nil:
			summary.Status = InstanceStatusHealthy
		default:
			summary.Status = InstanceStatusUnknown
		}
		summaries = append(summaries, summary)
	}

	monitoring := InstanceMonitoringUnknown
	switch {
	case knownNodes == 0:
		monitoring = InstanceMonitoringUnknown
	case disabledNodes > 0:
		monitoring = InstanceMonitoringDisabled
	case enabledNodes > 0:
		monitoring = InstanceMonitoringEnabled
	}

	return InstanceResourceSummaryResponse{
		Instances:   summaries,
		GeneratedAt: now,
		Monitoring:  monitoring,
	}
}

// instanceResourceUsage extracts the approved CPU/memory/storage percentages
// from a stored instance's decoded info payload (schema of SystemInstanceInfo:
// resources.cpu.usage_percent, resources.memory.usage_percent,
// resources.storage.used_percent), together with the node's own monitor state.
//
// Validity is recorded by the sampling node alongside the value, so it is per
// resource and per sample:
//   - an explicit `available: false` means disabled or unsampled -> nil/unknown;
//   - an explicit `available: true` keeps the value even when it is 0, because a
//     genuinely idle CPU reports 0%;
//   - legacy payloads that omit the flag fall back conservatively to the
//     positive-only reading, so an untagged 0 is treated as unknown.
//
// Disk is sampled independently of the CPU/memory monitor, so storage can be
// known while both others are unknown.
func instanceResourceUsage(instance *SystemInstance) (InstanceResourceUsage, bool, bool) {
	info, ok := decodeSystemInstanceInfo(instance.Info).(map[string]any)
	if !ok {
		return InstanceResourceUsage{}, false, false
	}
	resources, ok := info["resources"].(map[string]any)
	if !ok {
		return InstanceResourceUsage{}, false, false
	}
	usage := InstanceResourceUsage{
		CPUPercent:     availablePercent(resources, "cpu", "usage_percent"),
		MemoryPercent:  availablePercent(resources, "memory", "usage_percent"),
		StoragePercent: availablePercent(resources, "storage", "used_percent"),
	}
	// The third result reports whether the sampling node recorded its monitor
	// state at all. Only an explicit false means monitoring was disabled there;
	// an absent key is a legacy payload and is not reported as disabled.
	monitoring, tagged := resources["monitoring"].(bool)
	if !tagged {
		return usage, false, false
	}
	return usage, !monitoring, true
}

// availablePercent reads one nested percentage, honouring the node's own
// validity flag. A missing value is always unknown.
func availablePercent(parent map[string]any, section string, field string) *float64 {
	child, ok := parent[section].(map[string]any)
	if !ok {
		return nil
	}
	value, ok := numberValue(child[field])
	if !ok {
		return nil
	}
	if available, tagged := child["available"].(bool); tagged {
		if !available {
			return nil
		}
		return &value
	}
	// Legacy payload without validity metadata: only a positive value is
	// trustworthy, because an untagged 0 also encodes the disabled monitor.
	if value <= 0 {
		return nil
	}
	return &value
}

func numberValue(raw any) (float64, bool) {
	switch v := raw.(type) {
	case float64:
		return v, true
	case int:
		return float64(v), true
	case int64:
		return float64(v), true
	case uint64:
		return float64(v), true
	default:
		return 0, false
	}
}

// apiEntryKey renders the stable public key for a configured API entry. It is
// derived from the persistent entry id only, so it survives reordering and never
// encodes the hostname-defaulted node name.
func apiEntryKey(id int) string {
	return "api-" + strconv.Itoa(id)
}
