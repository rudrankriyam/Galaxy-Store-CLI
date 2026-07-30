// Package reviews provides access to Galaxy Store buyer comments and replies.
package reviews

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"unicode/utf8"
)

const (
	commentPath      = "/seller/v2/content/comment"
	commentReplyPath = "/seller/v2/content/comment/reply"
	maxReplyBytes    = 1400
)

// JSONClient is the narrow Galaxy Store client surface used by this package.
type JSONClient interface {
	DoJSON(
		context.Context,
		string,
		string,
		any,
		any,
	) (*http.Response, error)
}

// Service manages Galaxy Store buyer comments and seller replies.
type Service struct {
	client JSONClient
}

// New creates a buyer-comment service.
func New(client JSONClient) (*Service, error) {
	if client == nil {
		return nil, errors.New("client is required")
	}
	return &Service{client: client}, nil
}

// ListOptions selects a page of comments or one exact comment. Samsung
// documents these parameters in a JSON body on the GET request.
type ListOptions struct {
	ContentID string
	CommentID string
	Page      int
}

type listRequest struct {
	ContentID string `json:"contentId"`
	CommentID string `json:"commentId,omitempty"`
	Page      int    `json:"pageNo,omitempty"`
}

// ReplyRequest identifies a buyer comment and contains the seller's response.
type ReplyRequest struct {
	ContentID   string `json:"contentId"`
	CommentID   string `json:"commentId"`
	CountryCode string `json:"countryCode"`
	ReplyText   string `json:"replyText"`
}

// DeleteReplyRequest identifies a seller reply to delete.
type DeleteReplyRequest struct {
	ReplyID string `json:"replyId"`
}

// ListResult is Samsung's complete response to View Buyer Comments. Raw
// preserves fields added by Samsung for lossless JSON output.
type ListResult struct {
	ResultCode    string          `json:"resultCode"`
	ResultMessage string          `json:"resultMessage"`
	Data          CommentPage     `json:"data"`
	Raw           json.RawMessage `json:"-"`
}

// CommentPage describes the page returned by Samsung.
type CommentPage struct {
	ContentID  string          `json:"contentId"`
	TotalCount int             `json:"totalCount"`
	Page       int             `json:"pageNo"`
	TotalPages int             `json:"totalPage"`
	Comments   []Comment       `json:"comments"`
	Raw        json.RawMessage `json:"-"`
}

// Comment is the stable subset of a Galaxy Store buyer comment. Rating uses
// Samsung's 1-10 half-star scale and is nil when the buyer did not rate the
// app. Raw preserves the complete comment object.
type Comment struct {
	CommentID   string          `json:"commentId"`
	CountryCode string          `json:"countryCode"`
	BuyerID     string          `json:"buyerId"`
	Rating      *int            `json:"rating"`
	Date        string          `json:"date"`
	CommentText string          `json:"commentText"`
	CountryName string          `json:"countryName"`
	Device      string          `json:"device"`
	AppVersion  string          `json:"appVersion,omitempty"`
	ReplyID     string          `json:"replyId,omitempty"`
	ReplyText   string          `json:"replyText,omitempty"`
	Raw         json.RawMessage `json:"-"`
}

// MutationResult is Samsung's complete response to adding or deleting a
// seller reply. Raw preserves any additional response fields.
type MutationResult struct {
	ResultCode    string          `json:"resultCode"`
	ResultMessage string          `json:"resultMessage"`
	Raw           json.RawMessage `json:"-"`
}

// List retrieves buyer comments. Unlike most GET APIs, Samsung's reference
// documents contentId, commentId, and pageNo in a JSON request body.
func (service *Service) List(ctx context.Context, options ListOptions) (*ListResult, error) {
	if err := validateListOptions(options); err != nil {
		return nil, err
	}

	request := listRequest(options)
	var result ListResult
	if _, err := service.client.DoJSON(
		ctx,
		http.MethodGet,
		commentPath,
		request,
		&result,
	); err != nil {
		return nil, fmt.Errorf("list Galaxy Store buyer comments: %w", err)
	}
	return &result, nil
}

// Reply adds one seller reply to a buyer comment. Samsung permits only one
// reply per comment. This method performs exactly one mutation request and
// leaves retry policy to the shared client, which never retries mutations.
func (service *Service) Reply(
	ctx context.Context,
	request ReplyRequest,
) (*MutationResult, error) {
	if err := validateReplyRequest(request); err != nil {
		return nil, err
	}

	var result MutationResult
	if _, err := service.client.DoJSON(
		ctx,
		http.MethodPost,
		commentReplyPath,
		request,
		&result,
	); err != nil {
		return nil, fmt.Errorf("reply to Galaxy Store buyer comment: %w", err)
	}
	return &result, nil
}

// DeleteReply removes one seller reply. Updating a reply is not supported by
// Samsung; callers must explicitly delete it and then create a new reply.
func (service *Service) DeleteReply(
	ctx context.Context,
	request DeleteReplyRequest,
) (*MutationResult, error) {
	if err := validateDeleteReplyRequest(request); err != nil {
		return nil, err
	}

	var result MutationResult
	if _, err := service.client.DoJSON(
		ctx,
		http.MethodDelete,
		commentReplyPath,
		request,
		&result,
	); err != nil {
		return nil, fmt.Errorf("delete Galaxy Store buyer comment reply: %w", err)
	}
	return &result, nil
}

func validateListOptions(options ListOptions) error {
	if err := validateContentID(options.ContentID); err != nil {
		return err
	}
	if options.CommentID != "" {
		if err := validateCommentID(options.CommentID); err != nil {
			return err
		}
	}
	if options.Page < 0 {
		return errors.New("page number cannot be negative")
	}
	return nil
}

func validateReplyRequest(request ReplyRequest) error {
	if err := validateContentID(request.ContentID); err != nil {
		return err
	}
	if err := validateCommentID(request.CommentID); err != nil {
		return err
	}
	if len(request.CountryCode) != 3 {
		return errors.New("country code must contain exactly 3 uppercase letters")
	}
	for _, character := range request.CountryCode {
		if character < 'A' || character > 'Z' {
			return errors.New("country code must contain exactly 3 uppercase letters")
		}
	}
	if !utf8.ValidString(request.ReplyText) {
		return errors.New("reply text must be valid UTF-8")
	}
	if strings.TrimSpace(request.ReplyText) == "" {
		return errors.New("reply text is required")
	}
	if len([]byte(request.ReplyText)) > maxReplyBytes {
		return errors.New("reply text cannot exceed 1400 bytes")
	}
	return nil
}

func validateDeleteReplyRequest(request DeleteReplyRequest) error {
	if err := validateNumericID("reply ID", request.ReplyID, 0); err != nil {
		return err
	}
	return nil
}

func validateContentID(contentID string) error {
	return validateNumericID("content ID", contentID, 12)
}

func validateCommentID(commentID string) error {
	return validateNumericID("comment ID", commentID, 7)
}

func validateNumericID(name string, value string, exactLength int) error {
	if value == "" || value != strings.TrimSpace(value) {
		if exactLength > 0 {
			return fmt.Errorf("%s must contain exactly %d digits", name, exactLength)
		}
		return fmt.Errorf("%s must contain only digits", name)
	}
	if exactLength > 0 && len(value) != exactLength {
		return fmt.Errorf("%s must contain exactly %d digits", name, exactLength)
	}
	for _, character := range value {
		if character < '0' || character > '9' {
			if exactLength > 0 {
				return fmt.Errorf("%s must contain exactly %d digits", name, exactLength)
			}
			return fmt.Errorf("%s must contain only digits", name)
		}
	}
	return nil
}

// UnmarshalJSON accepts either the string or numeric contentId shape described
// in Samsung's response documentation.
func (page *CommentPage) UnmarshalJSON(data []byte) error {
	raw := append(json.RawMessage(nil), data...)
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(data, &fields); err != nil {
		return err
	}

	contentID, err := stringOrNumber(fields["contentId"])
	if err != nil {
		return fmt.Errorf("decode contentId: %w", err)
	}
	var decoded struct {
		TotalCount int       `json:"totalCount"`
		Page       int       `json:"pageNo"`
		TotalPages int       `json:"totalPage"`
		Comments   []Comment `json:"comments"`
	}
	if err := json.Unmarshal(data, &decoded); err != nil {
		return err
	}
	*page = CommentPage{
		ContentID:  contentID,
		TotalCount: decoded.TotalCount,
		Page:       decoded.Page,
		TotalPages: decoded.TotalPages,
		Comments:   decoded.Comments,
		Raw:        raw,
	}
	if page.Comments == nil {
		page.Comments = []Comment{}
	}
	return nil
}

// MarshalJSON emits Samsung's original page object when available.
func (page CommentPage) MarshalJSON() ([]byte, error) {
	if len(page.Raw) != 0 {
		return validRawJSON("comment page", page.Raw)
	}
	type pageAlias CommentPage
	return json.Marshal(pageAlias(page))
}

// UnmarshalJSON preserves the original comment while decoding nullable fields.
func (comment *Comment) UnmarshalJSON(data []byte) error {
	raw := append(json.RawMessage(nil), data...)
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(data, &fields); err != nil {
		return err
	}

	var decoded Comment
	for _, field := range []struct {
		name   string
		target *string
	}{
		{name: "commentId", target: &decoded.CommentID},
		{name: "countryCode", target: &decoded.CountryCode},
		{name: "buyerId", target: &decoded.BuyerID},
		{name: "date", target: &decoded.Date},
		{name: "commentText", target: &decoded.CommentText},
		{name: "countryName", target: &decoded.CountryName},
		{name: "device", target: &decoded.Device},
		{name: "appVersion", target: &decoded.AppVersion},
		{name: "replyId", target: &decoded.ReplyID},
		{name: "replyText", target: &decoded.ReplyText},
	} {
		value, err := stringOrNumber(fields[field.name])
		if err != nil {
			return fmt.Errorf("decode %s: %w", field.name, err)
		}
		*field.target = value
	}
	if rating := bytes.TrimSpace(fields["rating"]); len(rating) != 0 && !bytes.Equal(rating, []byte("null")) {
		var value int
		if err := json.Unmarshal(rating, &value); err != nil {
			return fmt.Errorf("decode rating: %w", err)
		}
		decoded.Rating = &value
	}
	decoded.Raw = raw
	*comment = decoded
	return nil
}

// MarshalJSON emits Samsung's original comment object when available.
func (comment Comment) MarshalJSON() ([]byte, error) {
	if len(comment.Raw) != 0 {
		return validRawJSON("comment", comment.Raw)
	}
	type commentAlias Comment
	return json.Marshal(commentAlias(comment))
}

// UnmarshalJSON preserves the complete list response.
func (result *ListResult) UnmarshalJSON(data []byte) error {
	type resultAlias ListResult
	var decoded resultAlias
	if err := json.Unmarshal(data, &decoded); err != nil {
		return err
	}
	*result = ListResult(decoded)
	result.Raw = append(json.RawMessage(nil), data...)
	return nil
}

// MarshalJSON emits Samsung's original list response when available.
func (result ListResult) MarshalJSON() ([]byte, error) {
	if len(result.Raw) != 0 {
		return validRawJSON("buyer comments response", result.Raw)
	}
	type resultAlias ListResult
	return json.Marshal(resultAlias(result))
}

// UnmarshalJSON preserves the complete mutation response.
func (result *MutationResult) UnmarshalJSON(data []byte) error {
	type resultAlias MutationResult
	var decoded resultAlias
	if err := json.Unmarshal(data, &decoded); err != nil {
		return err
	}
	*result = MutationResult(decoded)
	result.Raw = append(json.RawMessage(nil), data...)
	return nil
}

// MarshalJSON emits Samsung's original mutation response when available.
func (result MutationResult) MarshalJSON() ([]byte, error) {
	if len(result.Raw) != 0 {
		return validRawJSON("buyer comment mutation response", result.Raw)
	}
	type resultAlias MutationResult
	return json.Marshal(resultAlias(result))
}

func stringOrNumber(raw json.RawMessage) (string, error) {
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 || bytes.Equal(trimmed, []byte("null")) {
		return "", nil
	}
	var value string
	if err := json.Unmarshal(trimmed, &value); err == nil {
		return value, nil
	}
	decoder := json.NewDecoder(bytes.NewReader(trimmed))
	decoder.UseNumber()
	var number json.Number
	if err := decoder.Decode(&number); err == nil {
		return number.String(), nil
	}
	return "", errors.New("expected a string or number")
}

func validRawJSON(name string, raw json.RawMessage) ([]byte, error) {
	if !json.Valid(raw) {
		return nil, fmt.Errorf("%s raw response is invalid JSON", name)
	}
	return append([]byte(nil), raw...), nil
}
