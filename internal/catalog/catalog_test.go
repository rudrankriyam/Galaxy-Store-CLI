package catalog

import (
	"strings"
	"testing"
)

func TestLoadEmbeddedCatalog(t *testing.T) {
	t.Parallel()

	got, err := Load()
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if got.SchemaVersion != 1 {
		t.Fatalf("SchemaVersion = %d, want 1", got.SchemaVersion)
	}
	if len(got.Operations) != 39 {
		t.Fatalf("len(Operations) = %d, want 39", len(got.Operations))
	}
	if len(got.Limitations) != 6 {
		t.Fatalf("len(Limitations) = %d, want 6", len(got.Limitations))
	}
}

func TestCatalogCommandsMatchCurrentCLI(t *testing.T) {
	t.Parallel()

	got, err := Load()
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	want := map[string]string{
		"auth.token.create":     "gsc auth login --profile <name> --service-account-id <service-account-id> --private-key <path> --scope <scope>",
		"auth.token.revoke":     "gsc auth revoke --profile <name> --confirm",
		"content.binary.add":    "gsc binaries add --content-id <content-id> --file-key <file-key> --gms <Y|N> --confirm",
		"content.binary.update": "gsc binaries update --content-id <content-id> --binary-seq <sequence> --gms <Y|N> --confirm",
		"beta.view":             "gsc beta testers list --content-id <content-id> --app-status <SALE|REGISTRATION>",
		"rollout.rate.view":     "gsc rollouts rate view --content-id <content-id> --app-status <SALE|REGISTRATION>",
		"rollout.rate.update":   "gsc rollouts rate update --content-id <content-id> --app-status <SALE|REGISTRATION> --file <rates.json> --confirm",
		"rollout.rate.complete": "gsc rollouts rate complete --content-id <content-id> --app-status <SALE|REGISTRATION> --confirm",
		"rollout.binary.list":   "gsc rollouts binaries list --content-id <content-id> --app-status <SALE|REGISTRATION>",
		"reviews.reply":         "gsc reviews reply --content-id <content-id> --comment-id <comment-id> --country-code <code> --body <text> --confirm",
		"upload.session.create": "gsc uploads sessions create --confirm",
		"upload.file":           "gsc uploads file --session-id <session-id> --file <path> --confirm",
		"iap.orders.list":       "gsc iap orders list --seller-seq <seller-seq> --request-date <YYYYMMDD>",
		"gss.content.query":     "gsc stats content --file <query.json>",
	}
	for id, command := range want {
		operation, ok := got.OperationByID(id)
		if !ok {
			t.Errorf("missing operation %q", id)
			continue
		}
		if operation.ProposedCommand != command {
			t.Errorf("%s ProposedCommand = %q, want %q", id, operation.ProposedCommand, command)
		}
	}
}

func TestEveryOperationHasCommandAndAuditFields(t *testing.T) {
	t.Parallel()

	got, err := Load()
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	for _, operation := range got.Operations {
		operation := operation
		t.Run(operation.ID, func(t *testing.T) {
			t.Parallel()
			if !strings.HasPrefix(operation.ProposedCommand, "gsc ") {
				t.Errorf("ProposedCommand = %q, want gsc command", operation.ProposedCommand)
			}
			if operation.SourceURL == "" || operation.LastVerified == "" {
				t.Error("operation is missing source audit fields")
			}
		})
	}
}

func TestCatalogCoversRequiredFamiliesAndSpecialHosts(t *testing.T) {
	t.Parallel()

	got, err := Load()
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	wantFamilies := []string{
		"auth", "content", "beta", "rollout", "reviews", "iap-items",
		"iap-purchases", "iap-subscriptions", "iap-orders", "iap-receipts",
		"gss", "upload",
	}
	for _, family := range wantFamilies {
		if len(got.OperationsByFamily(family)) == 0 {
			t.Errorf("missing family %q", family)
		}
	}

	upload, ok := got.OperationByID("upload.file")
	if !ok {
		t.Fatal("missing upload.file")
	}
	if upload.Host != "https://seller.samsungapps.com" {
		t.Errorf("upload host = %q, want seller upload host", upload.Host)
	}

	receipt, ok := got.OperationByID("iap.receipts.verify")
	if !ok {
		t.Fatal("missing iap.receipts.verify")
	}
	if receipt.Host != "https://iap.samsungapps.com" || receipt.Auth != "none" {
		t.Errorf("receipt host/auth = %q/%q, want public IAP receipt endpoint", receipt.Host, receipt.Auth)
	}
}

func TestPortalOnlyLimitationsAreExplicit(t *testing.T) {
	t.Parallel()

	got, err := Load()
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	want := []string{
		"portal.app-create", "portal.commercial-status", "portal.service-account",
		"portal.subscription-catalog", "portal.settlement-reports", "portal.categories",
	}
	for _, id := range want {
		found := false
		for _, limitation := range got.Limitations {
			if limitation.ID == id {
				found = true
				if !limitation.PortalOnly {
					t.Errorf("%s is not marked portal-only", id)
				}
			}
		}
		if !found {
			t.Errorf("missing portal-only limitation %q", id)
		}
	}
}

func TestOperationQueries(t *testing.T) {
	t.Parallel()

	got, err := Load()
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	operation, ok := got.OperationByID("content.binary.add")
	if !ok {
		t.Fatal("OperationByID(content.binary.add) not found")
	}
	if operation.Method != "POST" || operation.Path != "/seller/v2/content/binary" {
		t.Errorf("binary add = %s %s", operation.Method, operation.Path)
	}

	families := got.Families()
	for index := 1; index < len(families); index++ {
		if families[index-1] > families[index] {
			t.Fatalf("Families() is not sorted: %v", families)
		}
	}
}

func TestValidateRejectsDuplicateOperationIDs(t *testing.T) {
	t.Parallel()

	got, err := Load()
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	got.Operations = append(got.Operations, got.Operations[0])
	if err := got.Validate(); err == nil || !strings.Contains(err.Error(), "duplicate operation id") {
		t.Fatalf("Validate() error = %v, want duplicate operation id", err)
	}
}

func TestValidateRejectsInvalidMethodAndURLs(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		mutate  func(*Catalog)
		wantErr string
	}{
		{
			name: "method",
			mutate: func(catalog *Catalog) {
				catalog.Operations[0].Method = "TRACE"
			},
			wantErr: "invalid method",
		},
		{
			name: "operation host",
			mutate: func(catalog *Catalog) {
				catalog.Operations[0].Host = "http://example.com"
			},
			wantErr: "absolute HTTPS URL",
		},
		{
			name: "source URL",
			mutate: func(catalog *Catalog) {
				catalog.Operations[0].SourceURL = "not-a-url"
			},
			wantErr: "absolute HTTPS URL",
		},
	}

	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			got, err := Load()
			if err != nil {
				t.Fatalf("Load() error = %v", err)
			}
			test.mutate(got)
			if err := got.Validate(); err == nil || !strings.Contains(err.Error(), test.wantErr) {
				t.Fatalf("Validate() error = %v, want containing %q", err, test.wantErr)
			}
		})
	}
}

func TestValidateRejectsNonCanonicalGetLeaf(t *testing.T) {
	t.Parallel()

	got, err := Load()
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	got.Operations[0].ProposedCommand = "gsc apps get --content-id <content-id>"
	if err := got.Validate(); err == nil || !strings.Contains(err.Error(), "non-canonical get leaf") {
		t.Fatalf("Validate() error = %v, want non-canonical get leaf", err)
	}
}
