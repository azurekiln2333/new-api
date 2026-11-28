package model

import (
	"maps"
	"slices"
	"sync"
	"sync/atomic"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/pkg/jsplugin"
)

// TaskAliasTarget is one mapping-derived alias after cross-channel aggregation.
// Declared is empty when the same plugin resolves the alias to more than one
// declared tail (display-only; pin is disabled).
type TaskAliasTarget struct {
	Alias     string
	Declared  string
	PluginKey string
}

type taskAliasView struct {
	generation uint64
	// channelRevision is the channel-table revision this view was built from. A
	// management write bumps it instead of rebuilding the view, so a stale view is
	// rejected on the next read rather than through a full enabled-channel scan on
	// the save path.
	channelRevision uint64
	expiresAt       time.Time
	byExact         map[string]TaskAliasTarget
	byFold          map[string]TaskAliasTarget
}

const taskAliasViewTTL = 60 * time.Second

// taskAliasChannelRevision advances on every committed channel write that can
// change an alias: model_mapping, models, status, type or deletion. A view built
// from an older revision is rejected and rebuilt lazily by the first reader.
var taskAliasChannelRevision atomic.Uint64

// BumpTaskAliasChannelRevision invalidates every published alias view without
// scanning the channel table. It is called from the channel write path, so a
// management save never pays for the full rebuild; the rebuild happens on the
// next alias lookup instead.
func BumpTaskAliasChannelRevision() {
	taskAliasChannelRevision.Add(1)
}

var (
	taskAliasViewPtr   atomic.Pointer[taskAliasView]
	taskAliasRebuildMu sync.Mutex
)

// ResolveTaskModelAliasIdentity returns only an exact configured alias. Relay
// selection already holds the catalog spelling from authorization, so it must
// not substitute a different source from the folded alias index.
func ResolveTaskModelAliasIdentity(g *jsplugin.RoutingGeneration, name string) (TaskAliasTarget, bool) {
	if g == nil || name == "" {
		return TaskAliasTarget{}, false
	}
	view := loadFreshTaskAliasView(g)
	if view == nil {
		return TaskAliasTarget{}, false
	}
	target, ok := view.byExact[name]
	return target, ok
}

// ResolveTaskModelAlias resolves an exact configured alias first, then an
// unambiguous ASCII-folded alias for display and pricing projections.
func ResolveTaskModelAlias(g *jsplugin.RoutingGeneration, name string) (TaskAliasTarget, bool) {
	target, found := ResolveTaskModelAliasIdentity(g, name)
	if !found {
		if g == nil || name == "" {
			return TaskAliasTarget{}, false
		}
		view := loadFreshTaskAliasView(g)
		if view == nil {
			return TaskAliasTarget{}, false
		}
		target, found = view.byFold[jsplugin.ASCIIFold(name)]
	}
	if !found || target.Declared == "" || target.PluginKey == "" {
		return TaskAliasTarget{}, false
	}
	return target, true
}

// ResolveTaskPluginModel matches a client model name against a plugin's
// declared models as the host protocols do: an exact declaration or configured
// alias takes precedence over a case-folded declaration. A case variant is
// served under the declared spelling, while a channel alias keeps its exact
// public spelling. declared is the model the name stands for. A nil g matches
// exact names only.
func ResolveTaskPluginModel(g *jsplugin.RoutingGeneration, name string, models []string) (served, declared string, ok bool) {
	if slices.Contains(models, name) {
		return name, name, true
	}
	if target, found := ResolveTaskModelAliasIdentity(g, name); found {
		if target.Declared == "" || !slices.Contains(models, target.Declared) {
			return "", "", false
		}
		return target.Alias, target.Declared, true
	}
	if canonical, found := g.CanonicalModel(name); found {
		if !slices.Contains(models, canonical) {
			return "", "", false
		}
		return canonical, canonical, true
	}
	return "", "", false
}

func loadFreshTaskAliasView(g *jsplugin.RoutingGeneration) *taskAliasView {
	view := taskAliasViewPtr.Load()
	if taskAliasViewFresh(view, g.Number) {
		return view
	}
	taskAliasRebuildMu.Lock()
	defer taskAliasRebuildMu.Unlock()
	view = taskAliasViewPtr.Load()
	if taskAliasViewFresh(view, g.Number) {
		return view
	}
	rebuilt := buildTaskAliasView(g)
	taskAliasViewPtr.Store(rebuilt)
	return rebuilt
}

func taskAliasViewFresh(view *taskAliasView, generation uint64) bool {
	return view != nil &&
		view.generation == generation &&
		view.channelRevision == taskAliasChannelRevision.Load() &&
		time.Now().Before(view.expiresAt)
}

// rebuildTaskAliasView rebuilds the view eagerly. Only cache reloads call it;
// the channel write path bumps the revision instead, so a save never scans the
// whole enabled-channel table.
func rebuildTaskAliasView() {
	taskAliasRebuildMu.Lock()
	defer taskAliasRebuildMu.Unlock()
	taskAliasViewPtr.Store(buildTaskAliasView(jsplugin.DefaultRegistry.Generation()))
}

type taskAliasSpelling struct {
	byPlugin map[string]map[string]struct{}
	invalid  bool
}

type taskAliasDraft struct {
	bySpelling map[string]*taskAliasSpelling
}

func buildTaskAliasView(generation *jsplugin.RoutingGeneration) *taskAliasView {
	genNum := uint64(0)
	if generation != nil {
		genNum = generation.Number
	}
	view := &taskAliasView{
		generation:      genNum,
		channelRevision: taskAliasChannelRevision.Load(),
		expiresAt:       time.Now().Add(taskAliasViewTTL),
		byExact:         make(map[string]TaskAliasTarget),
		byFold:          make(map[string]TaskAliasTarget),
	}
	if DB == nil {
		return view
	}

	var channels []Channel
	err := DB.Select("id", "type", "models", "model_mapping").
		Where("status = ?", common.ChannelStatusEnabled).
		Find(&channels).Error
	if err != nil {
		common.SysError(common.LogText("rebuild task alias view: %s", err.Error()))
		return view
	}

	drafts := make(map[string]*taskAliasDraft)
	for i := range channels {
		channel := &channels[i]
		mappingJSON := channel.GetModelMapping()
		if mappingJSON == "" || mappingJSON == "{}" {
			continue
		}
		modelMap := make(map[string]string)
		if err := common.UnmarshalJsonStr(mappingJSON, &modelMap); err != nil {
			common.SysError(common.LogText("task alias view: channel %d model_mapping: %s", channel.Id, err.Error()))
			continue
		}
		inModels := make(map[string]struct{})
		for _, modelName := range channel.GetModels() {
			inModels[modelName] = struct{}{}
		}
		for alias, mapped := range modelMap {
			if mapped == "" {
				continue
			}
			if _, exposed := inModels[alias]; !exposed {
				continue
			}
			if len(generation.PluginsByModel(alias)) > 0 {
				continue
			}

			tail, cyclic := followChannelModelMapping(modelMap, alias)
			if cyclic {
				common.SysError(common.LogText("task alias mapping cycle dropped: channel=%d key=%q", channel.Id, alias))
				if _, foldedSource := generation.CanonicalModel(alias); foldedSource {
					fold := jsplugin.ASCIIFold(alias)
					draft := drafts[fold]
					if draft == nil {
						draft = &taskAliasDraft{bySpelling: make(map[string]*taskAliasSpelling)}
						drafts[fold] = draft
					}
					draft.bySpelling[alias] = &taskAliasSpelling{invalid: true}
				}
				continue
			}
			plugin, exactPluginTarget := generation.GetByModel(tail)
			if !exactPluginTarget {
				_, foldedSource := generation.CanonicalModel(alias)
				_, foldedTarget := generation.CanonicalModel(tail)
				if !foldedSource && !foldedTarget {
					continue
				}
				fold := jsplugin.ASCIIFold(alias)
				draft := drafts[fold]
				if draft == nil {
					draft = &taskAliasDraft{bySpelling: make(map[string]*taskAliasSpelling)}
					drafts[fold] = draft
				}
				draft.bySpelling[alias] = &taskAliasSpelling{invalid: true}
				continue
			}

			fold := jsplugin.ASCIIFold(alias)
			draft := drafts[fold]
			if draft == nil {
				draft = &taskAliasDraft{bySpelling: make(map[string]*taskAliasSpelling)}
				drafts[fold] = draft
			}
			spelling := draft.bySpelling[alias]
			if spelling == nil {
				spelling = &taskAliasSpelling{byPlugin: make(map[string]map[string]struct{})}
				draft.bySpelling[alias] = spelling
			}
			declareds := spelling.byPlugin[plugin.Meta.Key]
			if declareds == nil {
				declareds = make(map[string]struct{})
				spelling.byPlugin[plugin.Meta.Key] = declareds
			}
			declareds[tail] = struct{}{}
		}
	}

	for fold, draft := range drafts {
		spellings := slices.Sorted(maps.Keys(draft.bySpelling))
		for _, alias := range spellings {
			spelling := draft.bySpelling[alias]
			target := TaskAliasTarget{Alias: alias}
			if !spelling.invalid && len(spelling.byPlugin) == 1 {
				for pluginKey, declareds := range spelling.byPlugin {
					target.PluginKey = pluginKey
					if len(declareds) == 1 {
						for declared := range declareds {
							target.Declared = declared
						}
					}
				}
			}
			if target.Declared == "" {
				target.PluginKey = ""
			}
			view.byExact[alias] = target
		}
		if len(spellings) == 1 && view.byExact[spellings[0]].Declared != "" {
			view.byFold[fold] = view.byExact[spellings[0]]
			continue
		}
		// Distinct exact catalog aliases that fold together, or a configured
		// alias with an invalid/ambiguous destination, must never resolve via a
		// case-folded plugin declaration.
		view.byFold[fold] = TaskAliasTarget{Alias: spellings[0]}
	}
	return view
}

// followChannelModelMapping walks one channel's mapping the same way
// ModelMappedHelper does: visited-set cycle detection, self-map stops at
// the current hop, a non-self cycle is reported to the caller.
func followChannelModelMapping(modelMap map[string]string, start string) (string, bool) {
	current := start
	visited := map[string]bool{current: true}
	for {
		mapped, exists := modelMap[current]
		if !exists || mapped == "" {
			return current, false
		}
		if visited[mapped] {
			if mapped == current {
				return current, false
			}
			return "", true
		}
		visited[mapped] = true
		current = mapped
	}
}
