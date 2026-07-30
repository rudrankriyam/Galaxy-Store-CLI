package metadatacmd

import (
	"strconv"

	"github.com/rudrankriyam/Galaxy-Store-CLI/internal/metadata"
	"github.com/rudrankriyam/Galaxy-Store-CLI/internal/output"
)

type pullResult struct {
	Directory     string             `json:"directory"`
	ContentID     string             `json:"contentId"`
	AppStatus     metadata.AppStatus `json:"appStatus"`
	ContentStatus string             `json:"contentStatus,omitempty"`
	SourceSHA256  string             `json:"sourceSha256"`
	Files         []string           `json:"files"`
}

func (result pullResult) OutputHeaders() []string {
	return []string{"CONTENT ID", "APP STATUS", "CONTENT STATUS", "DIRECTORY"}
}

func (result pullResult) OutputRows() [][]string {
	return [][]string{{
		result.ContentID,
		string(result.AppStatus),
		result.ContentStatus,
		result.Directory,
	}}
}

type validationResult struct {
	Valid         bool               `json:"valid"`
	Directory     string             `json:"directory"`
	SchemaVersion int                `json:"schemaVersion"`
	ContentID     string             `json:"contentId"`
	AppStatus     metadata.AppStatus `json:"appStatus"`
	SourceSHA256  string             `json:"sourceSha256"`
}

func (result validationResult) OutputHeaders() []string {
	return []string{"VALID", "SCHEMA", "CONTENT ID", "APP STATUS", "DIRECTORY"}
}

func (result validationResult) OutputRows() [][]string {
	return [][]string{{
		strconv.FormatBool(result.Valid),
		strconv.Itoa(result.SchemaVersion),
		result.ContentID,
		string(result.AppStatus),
		result.Directory,
	}}
}

type planResult struct {
	ContentID            string             `json:"contentId"`
	AppStatus            metadata.AppStatus `json:"appStatus"`
	Changes              []metadata.Change  `json:"changes"`
	Destructive          bool               `json:"destructive"`
	RequiresConfirmation bool               `json:"requiresConfirmation"`
	MutationsPerformed   bool               `json:"mutationsPerformed"`
}

func newPlanResult(
	contentID string,
	appStatus metadata.AppStatus,
	plan metadata.Plan,
) planResult {
	return planResult{
		ContentID:            contentID,
		AppStatus:            appStatus,
		Changes:              plan.Changes,
		Destructive:          plan.HasDestructiveChanges(),
		RequiresConfirmation: plan.HasChanges(),
		MutationsPerformed:   false,
	}
}

func (result planResult) OutputHeaders() []string {
	return []string{"PATH", "KIND", "DESTRUCTIVE", "BEFORE", "AFTER"}
}

func (result planResult) OutputRows() [][]string {
	rows := make([][]string, 0, len(result.Changes))
	for _, change := range result.Changes {
		rows = append(rows, []string{
			change.Path,
			string(change.Kind),
			strconv.FormatBool(change.Destructive),
			string(change.Before),
			string(change.After),
		})
	}
	return rows
}

type applyResult struct {
	ContentID          string             `json:"contentId"`
	AppStatus          metadata.AppStatus `json:"appStatus"`
	Changes            []metadata.Change  `json:"changes"`
	Destructive        bool               `json:"destructive"`
	ResultCode         string             `json:"resultCode,omitempty"`
	ResultMessage      string             `json:"resultMessage,omitempty"`
	ReadbackVerified   bool               `json:"readbackVerified"`
	MutationsPerformed bool               `json:"mutationsPerformed"`
}

func (result applyResult) OutputHeaders() []string {
	return []string{
		"CONTENT ID",
		"APP STATUS",
		"CHANGES",
		"READBACK VERIFIED",
	}
}

func (result applyResult) OutputRows() [][]string {
	return [][]string{{
		result.ContentID,
		string(result.AppStatus),
		strconv.Itoa(len(result.Changes)),
		strconv.FormatBool(result.ReadbackVerified),
	}}
}

var (
	_ output.RowSource = pullResult{}
	_ output.RowSource = validationResult{}
	_ output.RowSource = planResult{}
	_ output.RowSource = applyResult{}
)
