package contentcmd

import (
	"github.com/rudrankriyam/Galaxy-Store-CLI/internal/cli/shared"
	"github.com/rudrankriyam/Galaxy-Store-CLI/internal/output"
	samsungcontent "github.com/rudrankriyam/Galaxy-Store-CLI/internal/samsung/content"
)

type planResult struct {
	shared.Plan
}

func (result planResult) OutputHeaders() []string {
	return []string{"ACTION", "RESOURCE", "DETAILS"}
}

func (result planResult) OutputRows() [][]string {
	rows := make([][]string, 0, len(result.Operations)+len(result.Warnings))
	for _, operation := range result.Operations {
		rows = append(rows, []string{operation.Action, operation.Resource, operation.Details})
	}
	for _, warning := range result.Warnings {
		rows = append(rows, []string{"warning", "", warning})
	}
	return rows
}

type mutationResult struct {
	Action         string `json:"action"`
	Resource       string `json:"resource"`
	Status         string `json:"status,omitempty"`
	BinarySequence string `json:"binarySequence,omitempty"`
	ResultCode     string `json:"resultCode,omitempty"`
	ResultMessage  string `json:"resultMessage,omitempty"`
}

func (result mutationResult) OutputHeaders() []string {
	return []string{"ACTION", "RESOURCE", "STATUS", "BINARY SEQUENCE", "RESULT"}
}

func (result mutationResult) OutputRows() [][]string {
	message := result.ResultCode
	if result.ResultMessage != "" {
		if message != "" {
			message += ": "
		}
		message += result.ResultMessage
	}
	return [][]string{{
		result.Action,
		result.Resource,
		result.Status,
		result.BinarySequence,
		message,
	}}
}

type uploadSessionResult struct {
	*samsungcontent.UploadSession
}

func (result uploadSessionResult) OutputHeaders() []string {
	return []string{"SESSION ID", "UPLOAD URL"}
}

func (result uploadSessionResult) OutputRows() [][]string {
	if result.UploadSession == nil {
		return nil
	}
	return [][]string{{result.SessionID, result.URL}}
}

type uploadFileResult struct {
	*samsungcontent.UploadResult
}

func (result uploadFileResult) OutputHeaders() []string {
	return []string{"FILE KEY", "FILE NAME", "FILE SIZE"}
}

func (result uploadFileResult) OutputRows() [][]string {
	if result.UploadResult == nil {
		return nil
	}
	return [][]string{{result.FileKey, result.FileName, result.FileSize}}
}

var (
	_ output.RowSource = planResult{}
	_ output.RowSource = mutationResult{}
	_ output.RowSource = uploadSessionResult{}
	_ output.RowSource = uploadFileResult{}
)
