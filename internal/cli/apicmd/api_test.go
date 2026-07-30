package apicmd

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/peterbourgon/ff/v3/ffcli"

	"github.com/rudrankriyam/Galaxy-Store-CLI/internal/output"
	"github.com/rudrankriyam/Galaxy-Store-CLI/internal/samsung"
)

func TestGETRequestNeedsNoConfirmationAndPreservesResponseJSON(t *testing.T) {
	var stdout bytes.Buffer
	client := &fakeClient{
		response: &Response{
			StatusCode: http.StatusOK,
			Body:       json.RawMessage(`{"unknown":{"nested":true},"nullable":null}`),
		},
	}
	dependencies, openCalls := testDependencies(&stdout, client)

	err := execute(
		NewCommand(dependencies),
		"request",
		"--method", "get",
		"--path", "/seller/contentInfo?contentId=000007654321",
		"--profile", "production",
		"--output", "json",
	)
	if err != nil {
		t.Fatalf("request error = %v", err)
	}
	if *openCalls != 1 || client.calls != 1 {
		t.Fatalf("open calls = %d, client calls = %d", *openCalls, client.calls)
	}
	if client.method != http.MethodGet ||
		client.path != "/seller/contentInfo?contentId=000007654321" ||
		len(client.body) != 0 {
		t.Fatalf("request = %s %s body=%q", client.method, client.path, client.body)
	}
	if !strings.Contains(stdout.String(), `"unknown":{"nested":true}`) ||
		!strings.Contains(stdout.String(), `"nullable":null`) {
		t.Fatalf("stdout = %q", stdout.String())
	}
}

func TestRequestReadsJSONFileWithoutProjectingUnknownOrTriStateFields(t *testing.T) {
	const input = "{\n  \"unknown\": {\"keep\": true},\n  \"nullValue\": null,\n  \"empty\": []\n}\n"
	var stdout bytes.Buffer
	client := &fakeClient{response: &Response{StatusCode: http.StatusOK, Body: json.RawMessage(`{"ok":true}`)}}
	dependencies, _ := testDependencies(&stdout, client)
	var readPath string
	dependencies.ReadFile = func(path string) ([]byte, error) {
		readPath = path
		return []byte(input), nil
	}

	err := execute(
		NewCommand(dependencies),
		"request",
		"--method", "GET",
		"--path", "/seller/v2/content/comment",
		"--file", "request.json",
	)
	if err != nil {
		t.Fatalf("request error = %v", err)
	}
	if readPath != "request.json" {
		t.Fatalf("read path = %q", readPath)
	}
	if string(client.body) != input {
		t.Fatalf("client body = %q, want exact input %q", client.body, input)
	}
}

func TestHEADRequestNeedsNoConfirmation(t *testing.T) {
	client := &fakeClient{response: &Response{StatusCode: http.StatusNoContent}}
	dependencies, _ := testDependencies(&bytes.Buffer{}, client)

	err := execute(
		NewCommand(dependencies),
		"request",
		"--method", "HEAD",
		"--path", "/seller/contentList",
	)
	if err != nil {
		t.Fatalf("request error = %v", err)
	}
	if client.calls != 1 || client.method != http.MethodHead {
		t.Fatalf("client calls = %d, method = %q", client.calls, client.method)
	}
}

func TestMutationMethodsRequireConfirmationBeforeOpeningSession(t *testing.T) {
	for _, method := range []string{
		http.MethodPost,
		http.MethodPut,
		http.MethodPatch,
		http.MethodDelete,
	} {
		t.Run(method, func(t *testing.T) {
			client := &fakeClient{response: &Response{StatusCode: http.StatusOK}}
			dependencies, openCalls := testDependencies(&bytes.Buffer{}, client)

			err := execute(
				NewCommand(dependencies),
				"request",
				"--method", method,
				"--path", "/seller/contentUpdate",
			)
			if err == nil || !strings.Contains(err.Error(), "explicit confirmation required") {
				t.Fatalf("error = %v", err)
			}
			if *openCalls != 0 || client.calls != 0 {
				t.Fatalf("open calls = %d, client calls = %d", *openCalls, client.calls)
			}
		})
	}
}

func TestConfirmedMutationIsSentExactlyOnce(t *testing.T) {
	for _, method := range []string{
		http.MethodPost,
		http.MethodPut,
		http.MethodPatch,
		http.MethodDelete,
	} {
		t.Run(method, func(t *testing.T) {
			client := &fakeClient{response: &Response{StatusCode: http.StatusOK, Body: json.RawMessage(`{}`)}}
			dependencies, _ := testDependencies(&bytes.Buffer{}, client)

			err := execute(
				NewCommand(dependencies),
				"request",
				"--method", method,
				"--path", "/seller/contentUpdate",
				"--confirm",
			)
			if err != nil {
				t.Fatalf("request error = %v", err)
			}
			if client.calls != 1 {
				t.Fatalf("client calls = %d, want 1", client.calls)
			}
		})
	}
}

func TestDryRunValidatesBodyAndPrintsPlanWithoutSession(t *testing.T) {
	var stdout bytes.Buffer
	client := &fakeClient{}
	dependencies, openCalls := testDependencies(&stdout, client)
	dependencies.ReadFile = func(string) ([]byte, error) {
		return []byte(`{"contentId":"000007654321"}`), nil
	}

	err := execute(
		NewCommand(dependencies),
		"request",
		"--method", "POST",
		"--path", "/seller/contentSubmit",
		"--file", "submit.json",
		"--dry-run",
		"--output", "json",
	)
	if err != nil {
		t.Fatalf("dry-run error = %v", err)
	}
	if *openCalls != 0 || client.calls != 0 {
		t.Fatalf("open calls = %d, client calls = %d", *openCalls, client.calls)
	}
	if !strings.Contains(stdout.String(), `"requiresConfirmation":true`) ||
		!strings.Contains(stdout.String(), `"mutationsPerformed":false`) {
		t.Fatalf("stdout = %q", stdout.String())
	}
}

func TestInvalidMethodsAndPathsAreRejectedBeforeFileOrSessionAccess(t *testing.T) {
	tests := []struct {
		name   string
		method string
		path   string
	}{
		{name: "missing method", path: "/seller/contentList"},
		{name: "options", method: "OPTIONS", path: "/seller/contentList"},
		{name: "trace", method: "TRACE", path: "/seller/contentList"},
		{name: "absolute URL", method: "GET", path: "https://devapi.samsungapps.com/seller/contentList"},
		{name: "external URL", method: "GET", path: "https://attacker.example/seller/contentList"},
		{name: "scheme relative", method: "GET", path: "//attacker.example/seller/contentList"},
		{name: "fragment", method: "GET", path: "/seller/contentList#private"},
		{name: "outside roots", method: "GET", path: "/admin/users"},
		{name: "token exchange", method: "POST", path: "/auth/accessToken"},
		{name: "upload endpoint", method: "POST", path: "/galaxyapi/fileUpload"},
		{name: "dot segment", method: "GET", path: "/seller/../auth/checkAccessToken"},
		{name: "encoded dot segment", method: "GET", path: "/seller/%2e%2e/auth/checkAccessToken"},
		{name: "encoded slash", method: "GET", path: "/seller/content%2Foutside"},
		{name: "encoded carriage return", method: "GET", path: "/seller/contentList%0d"},
		{name: "encoded newline", method: "GET", path: "/seller/contentList%0a"},
		{name: "authorization query", method: "GET", path: "/seller/contentList?authorization=Bearer-secret"},
		{name: "token query", method: "GET", path: "/seller/contentList?access_token=secret-token"},
		{name: "service account query", method: "GET", path: "/seller/contentList?service-account-id=secret-id"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			client := &fakeClient{}
			dependencies, openCalls := testDependencies(&bytes.Buffer{}, client)
			var readCalls int
			dependencies.ReadFile = func(string) ([]byte, error) {
				readCalls++
				return []byte(`{}`), nil
			}

			args := []string{"request"}
			if test.method != "" {
				args = append(args, "--method", test.method)
			}
			args = append(args, "--path", test.path, "--file", "payload.json")
			err := execute(NewCommand(dependencies), args...)
			if err == nil {
				t.Fatal("error = nil")
			}
			if *openCalls != 0 || client.calls != 0 || readCalls != 0 {
				t.Fatalf(
					"side effects: open=%d client=%d read=%d",
					*openCalls,
					client.calls,
					readCalls,
				)
			}
			assertNotContains(t, err.Error(), "Bearer-secret", "secret-token", "secret-id")
		})
	}
}

func TestHeaderOverrideFlagIsNotSupported(t *testing.T) {
	dependencies, openCalls := testDependencies(&bytes.Buffer{}, &fakeClient{})
	err := execute(
		NewCommand(dependencies),
		"request",
		"--method", "GET",
		"--path", "/seller/contentList",
		"--header", "Authorization: Bearer secret-token",
	)
	if err == nil {
		t.Fatal("error = nil")
	}
	if *openCalls != 0 {
		t.Fatalf("open calls = %d", *openCalls)
	}
	assertNotContains(t, err.Error(), "secret-token")
}

func TestInvalidJSONAndOversizeFilesHaveNoSessionSideEffects(t *testing.T) {
	tests := []struct {
		name string
		data []byte
	}{
		{name: "invalid", data: []byte(`{"missing":`)},
		{name: "multiple", data: []byte(`{} {}`)},
		{name: "oversize", data: bytes.Repeat([]byte(" "), maximumRequestFileSize+1)},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			client := &fakeClient{}
			dependencies, openCalls := testDependencies(&bytes.Buffer{}, client)
			dependencies.ReadFile = func(string) ([]byte, error) {
				return test.data, nil
			}
			err := execute(
				NewCommand(dependencies),
				"request",
				"--method", "POST",
				"--path", "/seller/contentUpdate",
				"--file", "payload.json",
				"--confirm",
			)
			if err == nil {
				t.Fatal("error = nil")
			}
			if *openCalls != 0 || client.calls != 0 {
				t.Fatalf("open calls = %d, client calls = %d", *openCalls, client.calls)
			}
		})
	}
}

func TestOutputValidationPrecedesFileRead(t *testing.T) {
	dependencies, openCalls := testDependencies(&bytes.Buffer{}, &fakeClient{})
	var readCalls int
	dependencies.ReadFile = func(string) ([]byte, error) {
		readCalls++
		return []byte(`{}`), nil
	}
	err := execute(
		NewCommand(dependencies),
		"request",
		"--method", "GET",
		"--path", "/seller/contentList",
		"--file", "payload.json",
		"--output", "xml",
	)
	if err == nil || !strings.Contains(err.Error(), "invalid output format") {
		t.Fatalf("error = %v", err)
	}
	if readCalls != 0 || *openCalls != 0 {
		t.Fatalf("read calls = %d, open calls = %d", readCalls, *openCalls)
	}
}

func TestSessionAndClientFailuresNeverPrintSuccess(t *testing.T) {
	t.Run("open error", func(t *testing.T) {
		var stdout bytes.Buffer
		dependencies, _ := testDependencies(&stdout, &fakeClient{})
		dependencies.OpenSession = func(string) (*Session, error) {
			return nil, errors.New("credentials unavailable")
		}
		err := execute(
			NewCommand(dependencies),
			"request",
			"--method", "GET",
			"--path", "/seller/contentList",
		)
		if err == nil || stdout.Len() != 0 {
			t.Fatalf("error = %v, stdout = %q", err, stdout.String())
		}
	})

	t.Run("client error", func(t *testing.T) {
		var stdout bytes.Buffer
		client := &fakeClient{err: errors.New("remote failure")}
		dependencies, _ := testDependencies(&stdout, client)
		err := execute(
			NewCommand(dependencies),
			"request",
			"--method", "GET",
			"--path", "/seller/contentList",
		)
		if err == nil || stdout.Len() != 0 {
			t.Fatalf("error = %v, stdout = %q", err, stdout.String())
		}
	})

	t.Run("nil response", func(t *testing.T) {
		var stdout bytes.Buffer
		dependencies, _ := testDependencies(&stdout, &fakeClient{})
		err := execute(
			NewCommand(dependencies),
			"request",
			"--method", "GET",
			"--path", "/seller/contentList",
		)
		if err == nil || stdout.Len() != 0 {
			t.Fatalf("error = %v, stdout = %q", err, stdout.String())
		}
	})
}

func TestTableOutputContainsOnlyResponseMetadataAndBody(t *testing.T) {
	var stdout bytes.Buffer
	client := &fakeClient{
		response: &Response{StatusCode: http.StatusOK, Body: json.RawMessage(`{"ok":true}`)},
	}
	dependencies, _ := testDependencies(&stdout, client)
	err := execute(
		NewCommand(dependencies),
		"request",
		"--method", "GET",
		"--path", "/seller/contentList",
		"--output", "table",
	)
	if err != nil {
		t.Fatalf("request error = %v", err)
	}
	for _, value := range []string{"METHOD", "PATH", "STATUS", "GET", "/seller/contentList", `{"ok":true}`} {
		if !strings.Contains(stdout.String(), value) {
			t.Fatalf("stdout = %q, missing %q", stdout.String(), value)
		}
	}
}

func TestDefaultFileReaderRejectsSymlinks(t *testing.T) {
	directory := t.TempDir()
	target := filepath.Join(directory, "payload.json")
	link := filepath.Join(directory, "payload-link.json")
	if err := os.WriteFile(target, []byte(`{"ok":true}`), 0o600); err != nil {
		t.Fatalf("write target: %v", err)
	}
	if err := os.Symlink(target, link); err != nil {
		t.Fatalf("create symlink: %v", err)
	}
	if _, err := readRegularJSONFile(link); err == nil || !strings.Contains(err.Error(), "symbolic link") {
		t.Fatalf("read error = %v", err)
	}
}

func TestSamsungAdapterUsesFixedHostAndNeverRetriesMutation(t *testing.T) {
	var attempts int
	var captured *http.Request
	httpClient := &http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
		attempts++
		captured = request
		return &http.Response{
			StatusCode: http.StatusServiceUnavailable,
			Header:     make(http.Header),
			Body:       io.NopCloser(strings.NewReader(`{"code":"TEMPORARY","message":"retry later"}`)),
			Request:    request,
		}, nil
	})}
	client, err := samsung.NewClient(
		httpClient,
		samsung.TokenProviderFunc(func(context.Context) (string, error) {
			return "secret-access-token", nil
		}),
		"service-account",
		samsung.WithMaxRetries(3),
	)
	if err != nil {
		t.Fatalf("NewClient error = %v", err)
	}
	adapter := samsungClient{client: client}
	_, err = adapter.Request(
		context.Background(),
		http.MethodPost,
		"/seller/contentUpdate",
		json.RawMessage(`{"nullable":null,"empty":[]}`),
	)
	if err == nil {
		t.Fatal("request error = nil")
	}
	if attempts != 1 {
		t.Fatalf("attempts = %d, want exactly 1", attempts)
	}
	if captured == nil || captured.URL.Host != "devapi.samsungapps.com" {
		t.Fatalf("captured URL = %v", captured)
	}
	if captured.Header.Get("Authorization") != "Bearer secret-access-token" ||
		captured.Header.Get(samsung.ServiceAccountIDHeader) != "service-account" {
		t.Fatal("adapter did not delegate authentication to Samsung client")
	}
	assertNotContains(t, err.Error(), "secret-access-token")
}

func TestSamsungAdapterRedactsAccessTokenEchoedByAPI(t *testing.T) {
	httpClient := &http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
		return &http.Response{
			StatusCode: http.StatusUnauthorized,
			Header:     make(http.Header),
			Body: io.NopCloser(strings.NewReader(
				`{"code":"AUTH_REQUIRE","message":"invalid secret-access-token"}`,
			)),
			Request: request,
		}, nil
	})}
	client, err := samsung.NewClient(
		httpClient,
		samsung.TokenProviderFunc(func(context.Context) (string, error) {
			return "secret-access-token", nil
		}),
		"service-account",
	)
	if err != nil {
		t.Fatalf("NewClient error = %v", err)
	}
	_, err = (samsungClient{client: client}).Request(
		context.Background(),
		http.MethodGet,
		"/seller/contentList",
		nil,
	)
	if err == nil {
		t.Fatal("request error = nil")
	}
	assertNotContains(t, err.Error(), "secret-access-token")
}

func testDependencies(stdout *bytes.Buffer, client Client) (Dependencies, *int) {
	openCalls := 0
	return Dependencies{
		Stderr:  &bytes.Buffer{},
		Printer: output.NewPrinter(stdout, func(io.Writer) bool { return false }),
		ReadFile: func(string) ([]byte, error) {
			return nil, errors.New("unexpected file read")
		},
		OpenSession: func(profile string) (*Session, error) {
			openCalls++
			return &Session{Client: client, Profile: profile}, nil
		},
	}, &openCalls
}

func execute(command *ffcli.Command, args ...string) error {
	if err := command.Parse(args); err != nil {
		return err
	}
	return command.Run(context.Background())
}

func assertNotContains(t *testing.T, value string, secrets ...string) {
	t.Helper()
	for _, secret := range secrets {
		if secret != "" && strings.Contains(value, secret) {
			t.Fatalf("value leaked %q: %s", secret, value)
		}
	}
}

type fakeClient struct {
	response *Response
	err      error
	calls    int
	method   string
	path     string
	body     json.RawMessage
}

func (client *fakeClient) Request(
	_ context.Context,
	method string,
	path string,
	body json.RawMessage,
) (*Response, error) {
	client.calls++
	client.method = method
	client.path = path
	client.body = append(json.RawMessage(nil), body...)
	return client.response, client.err
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (function roundTripFunc) RoundTrip(request *http.Request) (*http.Response, error) {
	return function(request)
}

func ExampleNewCommand() {
	fmt.Println("gsc api request --method GET --path /seller/contentList")
	// Output: gsc api request --method GET --path /seller/contentList
}
