package metadata

import (
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/rudrankriyam/Galaxy-Store-CLI/internal/samsung/apps"
)

const testContentID = "000007654321"

func TestSelectRecordRequiresExactVariant(t *testing.T) {
	t.Parallel()

	records := []apps.App{
		{ContentID: testContentID, AppStatus: "SALE"},
		{ContentID: testContentID, AppStatus: "REGISTRATION"},
		{ContentID: "000000000001", AppStatus: "SALE"},
	}

	_, err := SelectRecord(records, testContentID, "")
	if err == nil || !strings.Contains(err.Error(), "app status") {
		t.Fatalf("SelectRecord without status error = %v", err)
	}

	selected, err := SelectRecord(records, testContentID, AppStatusSale)
	if err != nil {
		t.Fatalf("SelectRecord SALE: %v", err)
	}
	if selected.AppStatus != "SALE" {
		t.Fatalf("selected status = %q, want SALE", selected.AppStatus)
	}

	selected, err = SelectRecord(
		records,
		testContentID,
		AppStatus(" registration "),
	)
	if err != nil {
		t.Fatalf("SelectRecord REGISTRATION: %v", err)
	}
	if selected.AppStatus != "REGISTRATION" {
		t.Fatalf("selected status = %q, want REGISTRATION", selected.AppStatus)
	}
}

func TestSelectRecordRejectsMissingDuplicateAndInvalidVariants(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		records  []apps.App
		status   AppStatus
		contains string
	}{
		{
			name:     "missing",
			records:  []apps.App{{ContentID: testContentID, AppStatus: "SALE"}},
			status:   AppStatusRegistration,
			contains: "no REGISTRATION",
		},
		{
			name: "duplicate",
			records: []apps.App{
				{ContentID: testContentID, AppStatus: "SALE"},
				{ContentID: testContentID, AppStatus: "SALE"},
			},
			status:   AppStatusSale,
			contains: "multiple SALE",
		},
		{
			name: "unsupported returned status",
			records: []apps.App{
				{ContentID: testContentID, AppStatus: "UNKNOWN"},
			},
			contains: "SALE or REGISTRATION",
		},
		{
			name:     "invalid selector",
			records:  []apps.App{{ContentID: testContentID, AppStatus: "SALE"}},
			status:   "DRAFT",
			contains: "SALE or REGISTRATION",
		},
	}

	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			_, err := SelectRecord(test.records, testContentID, test.status)
			if err == nil || !strings.Contains(err.Error(), test.contains) {
				t.Fatalf("error = %v, want containing %q", err, test.contains)
			}
		})
	}
}

func TestCompileUsesExplicitAllowlistAndPreservesTriState(t *testing.T) {
	t.Parallel()

	source := json.RawMessage(`{
		"contentId":"000007654321",
		"appStatus":"SALE",
		"contentStatus":"FOR_SALE",
		"applicationType":"android",
		"defaultLanguageCode":"ENG",
		"paid":"N",
		"publicationType":"03",
		"appTitle":"Example",
		"addLanguage":null,
		"screenshots":[],
		"sellCountryList":[{"countryCode":"USA","price":"0"}],
		"binaryList":[{"binarySeq":"1"}],
		"category":[{"name":"Tool"}],
		"futureResponseField":{"opaque":true}
	}`)

	envelope, err := Compile(source)
	if err != nil {
		t.Fatalf("Compile: %v", err)
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(envelope, &fields); err != nil {
		t.Fatalf("decode envelope: %v", err)
	}
	for _, excluded := range []string{
		"appStatus",
		"contentStatus",
		"applicationType",
		"binaryList",
		"category",
		"futureResponseField",
	} {
		if _, exists := fields[excluded]; exists {
			t.Fatalf("compiled envelope contains %q: %s", excluded, envelope)
		}
	}
	if got := string(fields["addLanguage"]); got != "null" {
		t.Fatalf("addLanguage = %s, want null", got)
	}
	if got := string(fields["screenshots"]); got != "[]" {
		t.Fatalf("screenshots = %s, want []", got)
	}
	if got := string(fields["sellCountryList"]); !strings.Contains(got, `"USA"`) {
		t.Fatalf("sellCountryList = %s", got)
	}
}

func TestAllowedUpdateFieldsReturnsSortedCopy(t *testing.T) {
	t.Parallel()

	first := AllowedUpdateFields()
	if len(first) < 20 {
		t.Fatalf("allowed field count = %d, want documented update surface", len(first))
	}
	if !slicesAreSorted(first) {
		t.Fatalf("fields are not sorted: %v", first)
	}
	first[0] = "changed"
	second := AllowedUpdateFields()
	if reflect.DeepEqual(first, second) {
		t.Fatal("AllowedUpdateFields returned shared mutable storage")
	}
}

func TestValidateEnvelopeRequiredAndNestedFields(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		envelope string
		contains string
	}{
		{
			name: "unknown top-level field",
			envelope: validEnvelope(
				`"contentStatus":"REGISTERING"`,
			),
			contains: "unsupported field",
		},
		{
			name:     "binary list case variant",
			envelope: validEnvelope(`"BinaryList":[]`),
			contains: "binaryList",
		},
		{
			name: "language missing title",
			envelope: validEnvelope(
				`"addLanguage":[{"languagecode":"DEU","description":"Text"}]`,
			),
			contains: "addLanguage[0].appTitle is required",
		},
		{
			name: "screenshot missing reuse",
			envelope: validEnvelope(
				`"screenshots":[{"screenshotKey":"key"}]`,
			),
			contains: "screenshots[0].reuseYn is required",
		},
		{
			name: "localized screenshot reuse wrong type",
			envelope: validEnvelope(
				`"addLanguage":[{
					"languagecode":"DEU",
					"description":"Text",
					"appTitle":"Titel",
					"screenshots":[{"reuseYn":"Y"}]
				}]`,
			),
			contains: "screenshots[0].reuseYn must be a boolean",
		},
		{
			name: "country missing code",
			envelope: validEnvelope(
				`"sellCountryList":[{"price":"0"}]`,
			),
			contains: "sellCountryList[0].countryCode is required",
		},
	}

	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			err := ValidateEnvelope(
				testContentID,
				json.RawMessage(test.envelope),
			)
			if err == nil || !strings.Contains(err.Error(), test.contains) {
				t.Fatalf("error = %v, want containing %q", err, test.contains)
			}
		})
	}

	valid := json.RawMessage(validEnvelope(`
		"addLanguage":null,
		"screenshots":[],
		"sellCountryList":[{"countryCode":"USA"}]
	`))
	if err := ValidateEnvelope(testContentID, valid); err != nil {
		t.Fatalf("ValidateEnvelope valid tri-state payload: %v", err)
	}
}

func TestCanonicalSHA256IgnoresFormattingAndObjectKeyOrder(t *testing.T) {
	t.Parallel()

	left, err := CanonicalSHA256(json.RawMessage(`{"b":2,"a":{"y":1,"x":0}}`))
	if err != nil {
		t.Fatalf("CanonicalSHA256 left: %v", err)
	}
	right, err := CanonicalSHA256(
		json.RawMessage("{\n  \"a\": {\"x\":0,\"y\":1},\n  \"b\": 2\n}"),
	)
	if err != nil {
		t.Fatalf("CanonicalSHA256 right: %v", err)
	}
	if left != right {
		t.Fatalf("hashes differ: %s != %s", left, right)
	}
}

func TestNewBundleAndVerifyDrift(t *testing.T) {
	t.Parallel()

	source := sourceForStatus("SALE", `"future":"preserved"`)
	var record apps.App
	if err := json.Unmarshal(source, &record); err != nil {
		t.Fatalf("decode app: %v", err)
	}
	pulledAt := time.Date(2026, 7, 30, 12, 30, 0, 0, time.FixedZone("IST", 5*60*60+30*60))
	bundle, err := NewBundle(record, pulledAt)
	if err != nil {
		t.Fatalf("NewBundle: %v", err)
	}
	if bundle.Manifest.PulledAt != "2026-07-30T07:00:00Z" {
		t.Fatalf("PulledAt = %q", bundle.Manifest.PulledAt)
	}
	if !strings.Contains(string(bundle.Source), `"future":"preserved"`) {
		t.Fatalf("source lost unknown field: %s", bundle.Source)
	}
	if strings.Contains(string(bundle.Metadata), `"future"`) {
		t.Fatalf("metadata contains unknown field: %s", bundle.Metadata)
	}

	reordered := json.RawMessage(`{
		"future":"preserved",
		"publicationType":"03",
		"paid":"N",
		"defaultLanguageCode":"ENG",
		"contentStatus":"FOR_SALE",
		"appStatus":"SALE",
		"contentId":"000007654321"
	}`)
	if err := VerifyDrift(bundle.Manifest, reordered); err != nil {
		t.Fatalf("VerifyDrift reordered source: %v", err)
	}

	changed := sourceForStatus("SALE", `"future":"changed"`)
	err = VerifyDrift(bundle.Manifest, changed)
	if !errors.Is(err, ErrDrift) {
		t.Fatalf("VerifyDrift error = %v, want ErrDrift", err)
	}
}

func validEnvelope(extra string) string {
	parts := []string{
		`"contentId":"000007654321"`,
		`"defaultLanguageCode":"ENG"`,
		`"paid":"N"`,
		`"publicationType":"03"`,
	}
	if strings.TrimSpace(extra) != "" {
		parts = append(parts, extra)
	}
	return "{" + strings.Join(parts, ",") + "}"
}

func sourceForStatus(status string, extra string) json.RawMessage {
	parts := []string{
		`"contentId":"000007654321"`,
		`"appStatus":"` + status + `"`,
		`"contentStatus":"FOR_SALE"`,
		`"defaultLanguageCode":"ENG"`,
		`"paid":"N"`,
		`"publicationType":"03"`,
	}
	if strings.TrimSpace(extra) != "" {
		parts = append(parts, extra)
	}
	return json.RawMessage("{" + strings.Join(parts, ",") + "}")
}

func slicesAreSorted(values []string) bool {
	for index := 1; index < len(values); index++ {
		if values[index-1] > values[index] {
			return false
		}
	}
	return true
}
