package metadata

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"slices"
)

// ChangeKind describes a semantic metadata operation.
type ChangeKind string

const (
	ChangeAdd     ChangeKind = "add"
	ChangeUpdate  ChangeKind = "update"
	ChangeReplace ChangeKind = "replace"
	ChangeClear   ChangeKind = "clear"
)

// Change describes one top-level contentUpdate field change.
type Change struct {
	Path        string          `json:"path"`
	Kind        ChangeKind      `json:"kind"`
	Destructive bool            `json:"destructive"`
	Before      json.RawMessage `json:"before,omitempty"`
	After       json.RawMessage `json:"after,omitempty"`
}

// Plan is a deterministic semantic diff between current contentInfo data and
// an editable metadata envelope.
type Plan struct {
	Changes []Change `json:"changes"`
}

// HasChanges reports whether applying the plan would mutate metadata.
func (plan Plan) HasChanges() bool {
	return len(plan.Changes) != 0
}

// HasDestructiveChanges reports whether the plan clears or removes existing
// collection content.
func (plan Plan) HasDestructiveChanges() bool {
	for _, change := range plan.Changes {
		if change.Destructive {
			return true
		}
	}
	return false
}

var triStateCollections = map[string]struct{}{
	"addLanguage":     {},
	"screenshots":     {},
	"sellCountryList": {},
}

var collectionIdentityFields = map[string]string{
	"addLanguage":        "languagecode",
	"sellCountryList":    "countryCode",
	"supportedLanguages": "",
	"notifyResult":       "",
}

// Diff compares live contentInfo source data with a desired envelope.
//
// Omitted desired fields are ignored. For Samsung's tri-state collections,
// null means preserve, [] means clear, and a populated array means replace.
func Diff(currentSource json.RawMessage, desired json.RawMessage) (Plan, error) {
	currentEnvelope, err := Compile(currentSource)
	if err != nil {
		return Plan{}, fmt.Errorf("compile current contentInfo source: %w", err)
	}
	current, err := decodeObject(currentEnvelope, "current metadata")
	if err != nil {
		return Plan{}, err
	}
	next, err := decodeObject(desired, "desired metadata")
	if err != nil {
		return Plan{}, err
	}
	contentID, err := requiredString(next, "contentId")
	if err != nil {
		return Plan{}, err
	}
	if err := ValidateEnvelope(contentID, desired); err != nil {
		return Plan{}, err
	}
	currentContentID, err := requiredString(current, "contentId")
	if err != nil {
		return Plan{}, err
	}
	if currentContentID != contentID {
		return Plan{}, errors.New(
			"desired metadata contentId does not match current contentInfo contentId",
		)
	}

	fields := make([]string, 0, len(next))
	for field := range next {
		fields = append(fields, field)
	}
	slices.Sort(fields)

	plan := Plan{Changes: make([]Change, 0)}
	for _, field := range fields {
		after := next[field]
		before, exists := current[field]

		if _, triState := triStateCollections[field]; triState &&
			bytes.Equal(bytes.TrimSpace(after), []byte("null")) {
			continue
		}
		equal, err := semanticEqual(before, exists, after)
		if err != nil {
			return Plan{}, fmt.Errorf("compare metadata field %q: %w", field, err)
		}
		if equal {
			continue
		}

		kind := ChangeUpdate
		if !exists {
			kind = ChangeAdd
		}
		destructive := false
		if afterArray, ok := decodeArray(after); ok {
			kind = ChangeReplace
			if len(afterArray) == 0 {
				kind = ChangeClear
				destructive = arrayHasValues(before)
			} else {
				destructive = collectionRemovesValues(field, before, afterArray)
			}
		}
		if field == "screenshots" && exists && kind == ChangeReplace {
			// Screenshot arrays are positional. Any non-identical replacement
			// can remove an existing image even when the lengths are equal.
			destructive = arrayHasValues(before)
		}

		change := Change{
			Path:        "/" + field,
			Kind:        kind,
			Destructive: destructive,
			After:       canonicalRaw(after),
		}
		if exists {
			change.Before = canonicalRaw(before)
		}
		plan.Changes = append(plan.Changes, change)
	}
	return plan, nil
}

func semanticEqual(
	before json.RawMessage,
	beforeExists bool,
	after json.RawMessage,
) (bool, error) {
	if !beforeExists {
		return false, nil
	}
	beforeValue, err := decodeJSON(before)
	if err != nil {
		return false, err
	}
	afterValue, err := decodeJSON(after)
	if err != nil {
		return false, err
	}
	return reflect.DeepEqual(beforeValue, afterValue), nil
}

func decodeArray(value json.RawMessage) ([]any, bool) {
	decoded, err := decodeJSON(value)
	if err != nil {
		return nil, false
	}
	array, ok := decoded.([]any)
	return array, ok
}

func arrayHasValues(value json.RawMessage) bool {
	array, ok := decodeArray(value)
	return ok && len(array) != 0
}

func collectionRemovesValues(
	field string,
	before json.RawMessage,
	after []any,
) bool {
	beforeArray, ok := decodeArray(before)
	if !ok || len(beforeArray) == 0 {
		return false
	}
	identity, classified := collectionIdentityFields[field]
	if !classified {
		return len(after) < len(beforeArray)
	}

	afterIdentities := make(map[string]struct{}, len(after))
	for _, item := range after {
		if value, ok := collectionIdentity(item, identity); ok {
			afterIdentities[value] = struct{}{}
		}
	}
	for _, item := range beforeArray {
		value, ok := collectionIdentity(item, identity)
		if !ok {
			return len(after) < len(beforeArray)
		}
		if _, exists := afterIdentities[value]; !exists {
			return true
		}
	}
	return false
}

func collectionIdentity(item any, field string) (string, bool) {
	if field == "" {
		value, ok := item.(string)
		return value, ok
	}
	object, ok := item.(map[string]any)
	if !ok {
		return "", false
	}
	value, ok := object[field].(string)
	return value, ok && value != ""
}

func canonicalRaw(value json.RawMessage) json.RawMessage {
	canonical, err := canonicalJSON(value)
	if err != nil {
		return append(json.RawMessage(nil), value...)
	}
	return canonical
}
