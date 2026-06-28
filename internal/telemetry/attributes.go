package telemetry

import (
	"math"
	"reflect"
	"sort"
	"strings"
)

const (
	newRelicEventRallyTry        = "RallyTry"
	newRelicEventRallyRoute      = "RallyRoute"
	newRelicEventRallyDiagnostic = "RallyDiagnostic"
	newRelicEventRallyFailure    = "RallyFailure"

	maxNewRelicAttributes         = 64
	newRelicAttributeKeyByteLimit = 255
	maxNewRelicAttributeKeyBytes  = newRelicAttributeKeyByteLimit - 1
)

var newRelicCustomEventNames = map[string]struct{}{
	newRelicEventRallyTry:        {},
	newRelicEventRallyRoute:      {},
	newRelicEventRallyDiagnostic: {},
	newRelicEventRallyFailure:    {},
}

var priorityAttributeKeys = []string{
	"relay_id",
	"run_id",
	"try_id",
	"rally_span_id",
	"rally_parent_span_id",
	"repo",
	"lap_id",
	"runner",
	"role",
	"operation",
	"duration_ms",
	"outcome",
	"failure_category",
	"recovery_classification",
	"agent_state",
}

func isNewRelicCustomEventName(name string) bool {
	_, ok := newRelicCustomEventNames[name]
	return ok
}

func buildFailureAttributes(evt FailureEvent) map[string]interface{} {
	return buildAttributes(evt.Tags, evt.Contexts)
}

func buildFailureAttributesWithFields(evt FailureEvent, fields map[string]interface{}) map[string]interface{} {
	return buildAttributePayload(evt.Tags, fields, evt.Contexts)
}

func buildEventAttributes(evt Event) map[string]interface{} {
	tags := cloneStringMap(evt.Tags)
	if evt.Level != "" {
		tags["level"] = string(evt.Level)
	}
	return buildAttributes(tags, evt.Contexts)
}

func buildEventAttributesWithFields(evt Event, fields map[string]interface{}) map[string]interface{} {
	tags := cloneStringMap(evt.Tags)
	if evt.Level != "" {
		tags["level"] = string(evt.Level)
	}
	return buildAttributePayload(tags, fields, evt.Contexts)
}

func buildFlatAttributes(fields map[string]interface{}) map[string]interface{} {
	return buildAttributePayload(nil, fields, nil)
}

func buildSpanAttributes(tags map[string]string, fields map[string]interface{}, contexts map[string]map[string]interface{}) map[string]interface{} {
	return buildAttributePayload(tags, fields, contexts)
}

func buildAttributes(tags map[string]string, contexts map[string]map[string]interface{}) map[string]interface{} {
	return buildAttributePayload(tags, nil, contexts)
}

func buildAttributePayload(tags map[string]string, fields map[string]interface{}, contexts map[string]map[string]interface{}) map[string]interface{} {
	candidates := make(map[string]interface{})

	tagCopy := cloneStringMap(tags)
	scrubStringMap(tagCopy)
	for _, key := range sortedStringMapKeys(tagCopy) {
		addAttributeCandidate(candidates, key, tagCopy[key])
	}

	fieldCopy := cloneAttributeMap(fields)
	scrubAttributeMap(fieldCopy)
	for _, key := range sortedAttributeMapKeys(fieldCopy) {
		addAttributeCandidate(candidates, key, fieldCopy[key])
	}

	contextCopy := cloneContextMaps(contexts)
	scrubContextMaps(contextCopy)
	for _, name := range sortedContextNames(contextCopy) {
		flattenAttributeValue(candidates, name, contextCopy[name])
	}

	return selectBudgetedAttributes(candidates)
}

func addAttributeCandidate(candidates map[string]interface{}, key string, value interface{}) {
	key = cleanAttributeKey(key)
	if !validAttributeKey(key) {
		return
	}
	scalar, ok := scalarAttributeValue(value)
	if !ok {
		return
	}
	if _, exists := candidates[key]; exists {
		return
	}
	candidates[key] = scalar
}

func flattenAttributeValue(candidates map[string]interface{}, prefix string, value interface{}) {
	prefix = cleanAttributeKey(prefix)
	if !validAttributeKey(prefix) {
		return
	}

	switch x := value.(type) {
	case map[string]interface{}:
		for _, key := range sortedAttributeMapKeys(x) {
			flattenAttributeValue(candidates, joinAttributeKey(prefix, key), x[key])
		}
	case map[string]string:
		for _, key := range sortedStringMapKeys(x) {
			flattenAttributeValue(candidates, joinAttributeKey(prefix, key), x[key])
		}
	default:
		addAttributeCandidate(candidates, prefix, value)
	}
}

func selectBudgetedAttributes(candidates map[string]interface{}) map[string]interface{} {
	selected := make([]string, 0, maxNewRelicAttributes)
	seen := make(map[string]struct{}, len(priorityAttributeKeys))

	for _, key := range priorityAttributeKeys {
		if _, ok := candidates[key]; !ok {
			continue
		}
		selected = append(selected, key)
		seen[key] = struct{}{}
	}

	remaining := make([]string, 0, len(candidates)-len(selected))
	for key := range candidates {
		if _, ok := seen[key]; ok {
			continue
		}
		remaining = append(remaining, key)
	}
	sort.Strings(remaining)

	for _, key := range remaining {
		if len(selected) >= maxNewRelicAttributes {
			break
		}
		selected = append(selected, key)
	}

	attrs := make(map[string]interface{}, len(selected))
	for _, key := range selected {
		attrs[key] = candidates[key]
	}
	return attrs
}

func scalarAttributeValue(value interface{}) (interface{}, bool) {
	if value == nil {
		return nil, false
	}

	v := reflect.ValueOf(value)
	switch v.Kind() {
	case reflect.String:
		return truncateValue(collapseHomePaths(v.String())), true
	case reflect.Bool:
		return v.Bool(), true
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		return v.Int(), true
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64, reflect.Uintptr:
		return v.Uint(), true
	case reflect.Float32, reflect.Float64:
		f := v.Float()
		if math.IsInf(f, 0) || math.IsNaN(f) {
			return nil, false
		}
		return f, true
	default:
		return nil, false
	}
}

func validAttributeKey(key string) bool {
	if key == "" {
		return false
	}
	if len(key) > maxNewRelicAttributeKeyBytes {
		return false
	}
	return !isSensitiveAttributeKey(key)
}

func cleanAttributeKey(key string) string {
	return strings.TrimSpace(key)
}

func joinAttributeKey(prefix, key string) string {
	key = cleanAttributeKey(key)
	if key == "" {
		return prefix
	}
	return prefix + "." + key
}

func isSensitiveAttributeKey(key string) bool {
	if isSensitiveKey(key) {
		return true
	}
	for _, part := range strings.Split(key, ".") {
		if isSensitiveKey(part) {
			return true
		}
	}
	return false
}

func cloneStringMap(in map[string]string) map[string]string {
	out := make(map[string]string, len(in))
	for k, v := range in {
		out[k] = v
	}
	return out
}

func cloneContextMaps(in map[string]map[string]interface{}) map[string]map[string]interface{} {
	out := make(map[string]map[string]interface{}, len(in))
	for k, v := range in {
		out[k] = cloneAttributeMap(v)
	}
	return out
}

func cloneAttributeMap(in map[string]interface{}) map[string]interface{} {
	out := make(map[string]interface{}, len(in))
	for k, v := range in {
		out[k] = cloneAttributeValue(v)
	}
	return out
}

func cloneAttributeValue(value interface{}) interface{} {
	switch x := value.(type) {
	case map[string]interface{}:
		return cloneAttributeMap(x)
	case map[string]string:
		return cloneStringMap(x)
	case map[string]map[string]interface{}:
		return cloneContextMaps(x)
	case []interface{}:
		out := make([]interface{}, len(x))
		for i, v := range x {
			out[i] = cloneAttributeValue(v)
		}
		return out
	default:
		return value
	}
}

func sortedStringMapKeys(m map[string]string) []string {
	keys := make([]string, 0, len(m))
	for key := range m {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

func sortedAttributeMapKeys(m map[string]interface{}) []string {
	keys := make([]string, 0, len(m))
	for key := range m {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

func sortedContextNames(m map[string]map[string]interface{}) []string {
	keys := make([]string, 0, len(m))
	for key := range m {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}
