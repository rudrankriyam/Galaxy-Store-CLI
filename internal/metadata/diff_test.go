package metadata

import (
	"encoding/json"
	"reflect"
	"testing"
)

func TestDiffTreatsNullAsPreserveAndEmptyAsClear(t *testing.T) {
	t.Parallel()

	current := sourceForStatus("SALE", `
		"addLanguage":[
			{"languagecode":"DEU","description":"Deutsch","appTitle":"App"}
		],
		"screenshots":[{"reuseYn":true}],
		"sellCountryList":[{"countryCode":"USA","price":"0"}]
	`)

	preserve := json.RawMessage(validEnvelope(`
		"addLanguage":null,
		"screenshots":null,
		"sellCountryList":null
	`))
	plan, err := Diff(current, preserve)
	if err != nil {
		t.Fatalf("Diff preserve: %v", err)
	}
	if plan.HasChanges() {
		t.Fatalf("preserve plan = %+v, want no changes", plan)
	}

	clear := json.RawMessage(validEnvelope(`
		"addLanguage":[],
		"screenshots":[],
		"sellCountryList":[]
	`))
	plan, err = Diff(current, clear)
	if err != nil {
		t.Fatalf("Diff clear: %v", err)
	}
	if len(plan.Changes) != 3 || !plan.HasDestructiveChanges() {
		t.Fatalf("clear plan = %+v", plan)
	}
	for _, change := range plan.Changes {
		if change.Kind != ChangeClear || !change.Destructive {
			t.Fatalf("clear change = %+v", change)
		}
	}
}

func TestDiffIsSemanticDeterministicAndIgnoresSourceOnlyFields(t *testing.T) {
	t.Parallel()

	current := json.RawMessage(`{
		"contentId":"000007654321",
		"appStatus":"SALE",
		"defaultLanguageCode":"ENG",
		"paid":"N",
		"publicationType":"03",
		"appTitle":"Same",
		"binaryList":[{"binarySeq":"1"}],
		"futureResponseField":true
	}`)
	desired := json.RawMessage(`{
		"publicationType":"03",
		"appTitle":"Same",
		"paid":"N",
		"defaultLanguageCode":"ENG",
		"contentId":"000007654321"
	}`)
	plan, err := Diff(current, desired)
	if err != nil {
		t.Fatalf("Diff: %v", err)
	}
	if plan.HasChanges() {
		t.Fatalf("plan = %+v, want no changes", plan)
	}

	changed := json.RawMessage(validEnvelope(`
		"shortDescription":"Short",
		"appTitle":"Changed"
	`))
	plan, err = Diff(current, changed)
	if err != nil {
		t.Fatalf("Diff changed: %v", err)
	}
	gotPaths := []string{plan.Changes[0].Path, plan.Changes[1].Path}
	wantPaths := []string{"/appTitle", "/shortDescription"}
	if !reflect.DeepEqual(gotPaths, wantPaths) {
		t.Fatalf("paths = %v, want %v", gotPaths, wantPaths)
	}
	if plan.Changes[0].Kind != ChangeUpdate ||
		plan.Changes[1].Kind != ChangeAdd {
		t.Fatalf("changes = %+v", plan.Changes)
	}
}

func TestDiffClassifiesCollectionRemovalAndScreenshotReplacement(t *testing.T) {
	t.Parallel()

	current := sourceForStatus("SALE", `
		"addLanguage":[
			{"languagecode":"DEU","description":"Deutsch","appTitle":"App"},
			{"languagecode":"FRA","description":"Français","appTitle":"App"}
		],
		"supportedLanguages":["DEU","ENG","FRA"],
		"screenshots":[
			{"screenshotPath":"one.png","reuseYn":true},
			{"screenshotPath":"two.png","reuseYn":true}
		]
	`)
	desired := json.RawMessage(validEnvelope(`
		"addLanguage":[
			{"languagecode":"DEU","description":"Neu","appTitle":"App"},
			{"languagecode":"ITA","description":"Italiano","appTitle":"App"}
		],
		"supportedLanguages":["DEU","ENG"],
		"screenshots":[
			{"screenshotPath":"one.png","reuseYn":true},
			{"screenshotKey":"replacement","reuseYn":false}
		]
	`))
	plan, err := Diff(current, desired)
	if err != nil {
		t.Fatalf("Diff: %v", err)
	}
	if len(plan.Changes) != 3 {
		t.Fatalf("changes = %+v", plan.Changes)
	}
	for _, change := range plan.Changes {
		if !change.Destructive {
			t.Fatalf("change = %+v, want destructive classification", change)
		}
	}
}

func TestDiffDetectsScreenshotOrder(t *testing.T) {
	t.Parallel()

	current := sourceForStatus("SALE", `
		"screenshots":[
			{"screenshotPath":"one.png","reuseYn":true},
			{"screenshotPath":"two.png","reuseYn":true}
		]
	`)
	desired := json.RawMessage(validEnvelope(`
		"screenshots":[
			{"screenshotPath":"two.png","reuseYn":true},
			{"screenshotPath":"one.png","reuseYn":true}
		]
	`))
	plan, err := Diff(current, desired)
	if err != nil {
		t.Fatalf("Diff: %v", err)
	}
	if len(plan.Changes) != 1 ||
		plan.Changes[0].Path != "/screenshots" ||
		!plan.Changes[0].Destructive {
		t.Fatalf("plan = %+v", plan)
	}
}
