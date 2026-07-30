package authcmd

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/rsa"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/peterbourgon/ff/v3/ffcli"
	"github.com/rudrankriyam/Galaxy-Store-CLI/internal/config"
	"github.com/rudrankriyam/Galaxy-Store-CLI/internal/credentials"
	"github.com/rudrankriyam/Galaxy-Store-CLI/internal/output"
	samsungauth "github.com/rudrankriyam/Galaxy-Store-CLI/internal/samsung/auth"
)

const (
	testJWT         = "signed.secret.jwt"
	testAccessToken = "secret-access-token"
)

var (
	commandTestKeyOnce sync.Once
	commandTestKey     *rsa.PrivateKey
	commandTestKeyErr  error
)

func testPrivateKey(t *testing.T) *rsa.PrivateKey {
	t.Helper()
	commandTestKeyOnce.Do(func() {
		commandTestKey, commandTestKeyErr = rsa.GenerateKey(rand.Reader, 2048)
	})
	if commandTestKeyErr != nil {
		t.Fatalf("generate RSA key: %v", commandTestKeyErr)
	}
	return commandTestKey
}

func TestLoginExchangesOnceStoresSecretAndPersistsOnlyMetadata(t *testing.T) {
	var stdout bytes.Buffer
	var saved *config.Config
	var signedConfig samsungauth.JWTConfig
	store := &fakeTokenStore{getErr: credentials.ErrTokenNotFound}
	client := &fakeClient{
		exchangeResponse: &samsungauth.AccessTokenResponse{
			OK: true,
			CreatedItem: samsungauth.CreatedAccessTokenItem{
				AccessToken: testAccessToken,
			},
		},
	}
	dependencies := baseDependencies(t, &stdout, client, store)
	dependencies.LoadConfig = func() (*config.Config, error) {
		return nil, config.ErrNotFound
	}
	dependencies.SaveConfig = func(value *config.Config) error {
		saved = cloneConfig(value)
		return nil
	}
	dependencies.SignJWT = func(value samsungauth.JWTConfig) (string, error) {
		signedConfig = value
		return testJWT, nil
	}

	err := execute(NewCommand(dependencies),
		"login",
		"--profile", "production",
		"--service-account-id", "service-account",
		"--private-key", "./seller-key.pem",
		"--scope", "publishing",
		"--scope", "gss",
		"--output", "json",
	)
	if err != nil {
		t.Fatalf("login error = %v", err)
	}
	if client.exchangeCalls != 1 || client.exchangeJWT != testJWT {
		t.Fatalf("exchange calls = %d, JWT = %q", client.exchangeCalls, client.exchangeJWT)
	}
	if store.setCalls != 1 || store.setProfile != "production" || store.setToken != testAccessToken {
		t.Fatalf("token store Set = (%q, %q), calls %d", store.setProfile, store.setToken, store.setCalls)
	}
	if saved == nil {
		t.Fatal("config was not saved")
	}
	profile := saved.Profiles["production"]
	if profile.ServiceAccountID != "service-account" {
		t.Fatalf("service account = %q", profile.ServiceAccountID)
	}
	if !filepath.IsAbs(profile.PrivateKeyPath) {
		t.Fatalf("private key path = %q, want absolute", profile.PrivateKeyPath)
	}
	if fmt.Sprint(profile.Scopes) != "[publishing gss]" {
		t.Fatalf("scopes = %v", profile.Scopes)
	}
	if saved.DefaultProfile != "production" {
		t.Fatalf("default profile = %q", saved.DefaultProfile)
	}
	if signedConfig.ServiceAccountID != "service-account" || len(signedConfig.PrivateKeyPEM) == 0 {
		t.Fatalf("sign config missing expected metadata: %#v", signedConfig)
	}
	assertNoSecrets(t, stdout.String(), testJWT, testAccessToken, string(signedConfig.PrivateKeyPEM))
	if !strings.Contains(stdout.String(), `"authenticated":true`) {
		t.Fatalf("stdout = %q", stdout.String())
	}
}

func TestLoginDryRunValidatesAndSignsWithoutRemoteOrLocalMutation(t *testing.T) {
	var stdout bytes.Buffer
	store := &fakeTokenStore{}
	client := &fakeClient{}
	dependencies := baseDependencies(t, &stdout, client, store)
	var openCalls int
	dependencies.OpenTokenStore = func() (credentials.TokenStore, error) {
		openCalls++
		return store, nil
	}
	var signCalls int
	dependencies.SignJWT = func(samsungauth.JWTConfig) (string, error) {
		signCalls++
		return testJWT, nil
	}
	var saveCalls int
	dependencies.SaveConfig = func(*config.Config) error {
		saveCalls++
		return nil
	}

	err := execute(NewCommand(dependencies),
		"login",
		"--profile", "production",
		"--service-account-id", "service-account",
		"--private-key", "./seller-key.pem",
		"--scope", "publishing",
		"--dry-run",
		"--output", "json",
	)
	if err != nil {
		t.Fatalf("login dry-run error = %v", err)
	}
	if signCalls != 1 {
		t.Fatalf("sign calls = %d, want 1", signCalls)
	}
	if client.exchangeCalls != 0 || openCalls != 0 || store.setCalls != 0 || saveCalls != 0 {
		t.Fatalf(
			"dry-run side effects: exchange=%d open=%d set=%d save=%d",
			client.exchangeCalls,
			openCalls,
			store.setCalls,
			saveCalls,
		)
	}
	assertNoSecrets(t, stdout.String(), testJWT, testAccessToken)
	if !strings.Contains(stdout.String(), `"mutationsPerformed":false`) {
		t.Fatalf("stdout = %q", stdout.String())
	}
}

func TestLoginRejectsInvalidScopeBeforeReadingConfigOrKey(t *testing.T) {
	dependencies := baseDependencies(t, &bytes.Buffer{}, &fakeClient{}, &fakeTokenStore{})
	var configCalls int
	dependencies.LoadConfig = func() (*config.Config, error) {
		configCalls++
		return nil, errors.New("must not load")
	}
	var keyCalls int
	dependencies.LoadPrivateKey = func(string) (*rsa.PrivateKey, error) {
		keyCalls++
		return nil, errors.New("must not read")
	}

	err := execute(NewCommand(dependencies),
		"login",
		"--profile", "production",
		"--service-account-id", "service-account",
		"--private-key", "./seller-key.pem",
		"--scope", "unknown",
	)
	if err == nil || !strings.Contains(err.Error(), "invalid scope") {
		t.Fatalf("error = %v", err)
	}
	if configCalls != 0 || keyCalls != 0 {
		t.Fatalf("config calls = %d, key calls = %d", configCalls, keyCalls)
	}
}

func TestLoginPrivateKeyFailureHasNoNetworkOrWriteSideEffects(t *testing.T) {
	client := &fakeClient{}
	store := &fakeTokenStore{}
	dependencies := baseDependencies(t, &bytes.Buffer{}, client, store)
	dependencies.LoadPrivateKey = func(string) (*rsa.PrivateKey, error) {
		return nil, credentials.ErrInsecurePrivateKey
	}
	var saveCalls int
	dependencies.SaveConfig = func(*config.Config) error {
		saveCalls++
		return nil
	}

	err := execute(NewCommand(dependencies),
		"login",
		"--profile", "production",
		"--service-account-id", "service-account",
		"--private-key", "./seller-key.pem",
		"--scope", "publishing",
	)
	if !errors.Is(err, credentials.ErrInsecurePrivateKey) {
		t.Fatalf("error = %v", err)
	}
	if client.exchangeCalls != 0 || store.setCalls != 0 || saveCalls != 0 {
		t.Fatalf("side effects: exchange=%d set=%d save=%d", client.exchangeCalls, store.setCalls, saveCalls)
	}
}

func TestLoginExchangeErrorIsRedactedAndDoesNotPersist(t *testing.T) {
	client := &fakeClient{exchangeErr: fmt.Errorf("server rejected %s", testJWT)}
	store := &fakeTokenStore{getErr: credentials.ErrTokenNotFound}
	dependencies := baseDependencies(t, &bytes.Buffer{}, client, store)
	var saveCalls int
	dependencies.SaveConfig = func(*config.Config) error {
		saveCalls++
		return nil
	}

	err := executeLogin(dependencies)
	if err == nil {
		t.Fatal("login error = nil")
	}
	assertNoSecrets(t, err.Error(), testJWT)
	if store.setCalls != 0 || saveCalls != 0 {
		t.Fatalf("set calls = %d, save calls = %d", store.setCalls, saveCalls)
	}
}

func TestLoginRejectsNilSuccessResponseWithoutPersisting(t *testing.T) {
	client := &fakeClient{}
	store := &fakeTokenStore{getErr: credentials.ErrTokenNotFound}
	dependencies := baseDependencies(t, &bytes.Buffer{}, client, store)
	var saveCalls int
	dependencies.SaveConfig = func(*config.Config) error {
		saveCalls++
		return nil
	}

	err := executeLogin(dependencies)
	if err == nil || !strings.Contains(err.Error(), "invalid access-token response") {
		t.Fatalf("error = %v", err)
	}
	if store.setCalls != 0 || saveCalls != 0 {
		t.Fatalf("set calls = %d, save calls = %d", store.setCalls, saveCalls)
	}
}

func TestLoginStoreFailureRevokesNewTokenAndDoesNotSaveConfig(t *testing.T) {
	client := &fakeClient{
		exchangeResponse: accessTokenResponse(),
		revokeResponse:   &samsungauth.TokenStatusResponse{OK: true},
	}
	store := &fakeTokenStore{
		getErr: credentials.ErrTokenNotFound,
		setErr: fmt.Errorf("could not store %s", testAccessToken),
	}
	dependencies := baseDependencies(t, &bytes.Buffer{}, client, store)
	var saveCalls int
	dependencies.SaveConfig = func(*config.Config) error {
		saveCalls++
		return nil
	}

	err := executeLogin(dependencies)
	if err == nil {
		t.Fatal("login error = nil")
	}
	assertNoSecrets(t, err.Error(), testAccessToken)
	if saveCalls != 0 {
		t.Fatalf("save calls = %d, want 0", saveCalls)
	}
	if client.revokeCalls != 1 || client.revokeToken != testAccessToken {
		t.Fatalf("cleanup revoke calls = %d, token = %q", client.revokeCalls, client.revokeToken)
	}
	if store.deleteCalls != 1 {
		t.Fatalf("cleanup delete calls = %d, want 1", store.deleteCalls)
	}
}

func TestLoginSaveFailureRestoresPreviousTokenAndRevokesNewToken(t *testing.T) {
	const previousToken = "previous-access-token"
	client := &fakeClient{
		exchangeResponse: accessTokenResponse(),
		revokeResponse:   &samsungauth.TokenStatusResponse{OK: true},
	}
	store := &fakeTokenStore{getToken: previousToken}
	dependencies := baseDependencies(t, &bytes.Buffer{}, client, store)
	dependencies.SaveConfig = func(*config.Config) error {
		return errors.New("disk full")
	}

	err := executeLogin(dependencies)
	if err == nil || !strings.Contains(err.Error(), "save profile metadata") {
		t.Fatalf("error = %v", err)
	}
	if fmt.Sprint(store.setTokens) != fmt.Sprint([]string{testAccessToken, previousToken}) {
		t.Fatalf("stored tokens = %v", store.setTokens)
	}
	if client.revokeCalls != 1 || client.revokeToken != testAccessToken {
		t.Fatalf("cleanup revoke calls = %d, token = %q", client.revokeCalls, client.revokeToken)
	}
}

func TestLoginRotationRevokesPreviousTokenAfterNewCredentialsAreDurable(t *testing.T) {
	const previousToken = "previous-access-token"
	var stdout bytes.Buffer
	var saved bool
	client := &fakeClient{
		exchangeResponse: accessTokenResponse(),
		revokeResponse:   &samsungauth.TokenStatusResponse{OK: true},
	}
	store := &fakeTokenStore{getToken: previousToken}
	dependencies := baseDependencies(t, &stdout, client, store)
	dependencies.SaveConfig = func(*config.Config) error {
		saved = true
		return nil
	}

	err := executeLogin(dependencies)
	if err != nil {
		t.Fatalf("login error = %v", err)
	}
	if !saved || store.setToken != testAccessToken {
		t.Fatalf("new credentials were not durable before rotation")
	}
	if client.revokeCalls != 1 || client.revokeToken != previousToken {
		t.Fatalf("revoke calls = %d, token = %q", client.revokeCalls, client.revokeToken)
	}
	if !strings.Contains(stdout.String(), `"replacedExistingToken":true`) ||
		!strings.Contains(stdout.String(), `"previousTokenRevoked":true`) {
		t.Fatalf("stdout = %q", stdout.String())
	}
	assertNoSecrets(t, stdout.String(), previousToken, testAccessToken)
}

func TestLoginRotationReportsUnconfirmedPreviousTokenWithoutRollingBackNewToken(t *testing.T) {
	const previousToken = "previous-access-token"
	client := &fakeClient{
		exchangeResponse: accessTokenResponse(),
		revokeErr:        fmt.Errorf("timeout revoking %s", previousToken),
	}
	store := &fakeTokenStore{getToken: previousToken}
	dependencies := baseDependencies(t, &bytes.Buffer{}, client, store)

	err := executeLogin(dependencies)
	if err == nil || !strings.Contains(err.Error(), "new credentials are active") {
		t.Fatalf("error = %v", err)
	}
	assertNoSecrets(t, err.Error(), previousToken, testAccessToken)
	if store.setToken != testAccessToken || store.deleteCalls != 0 {
		t.Fatalf("new token was unexpectedly rolled back: store = %#v", store)
	}
}

func TestLoginDefaultPrivateKeyLoaderRejectsInsecurePermissions(t *testing.T) {
	if os.PathSeparator == '\\' {
		t.Skip("Windows does not use Unix private-key permission bits")
	}
	path := filepath.Join(t.TempDir(), "seller-key.pem")
	if err := os.WriteFile(path, []byte("not relevant"), 0o644); err != nil {
		t.Fatalf("write key: %v", err)
	}

	client := &fakeClient{}
	dependencies := baseDependencies(t, &bytes.Buffer{}, client, &fakeTokenStore{})
	dependencies.LoadPrivateKey = credentials.LoadPrivateKey

	err := execute(NewCommand(dependencies),
		"login",
		"--profile", "production",
		"--service-account-id", "service-account",
		"--private-key", path,
		"--scope", "publishing",
	)
	if !errors.Is(err, credentials.ErrInsecurePrivateKey) {
		t.Fatalf("error = %v", err)
	}
	if client.exchangeCalls != 0 {
		t.Fatalf("exchange calls = %d", client.exchangeCalls)
	}
}

func TestStatusChecksResolvedAccessTokenWithoutOutputtingIt(t *testing.T) {
	var stdout bytes.Buffer
	client := &fakeClient{checkResponse: &samsungauth.TokenStatusResponse{OK: true}}
	store := &fakeTokenStore{getToken: testAccessToken}
	dependencies := baseDependencies(t, &stdout, client, store)

	err := execute(NewCommand(dependencies), "status", "--profile", "production", "--output", "json")
	if err != nil {
		t.Fatalf("status error = %v", err)
	}
	if client.checkCalls != 1 || client.checkToken != testAccessToken {
		t.Fatalf("check calls = %d, token = %q", client.checkCalls, client.checkToken)
	}
	assertNoSecrets(t, stdout.String(), testAccessToken)
	if !strings.Contains(stdout.String(), `"valid":true`) {
		t.Fatalf("stdout = %q", stdout.String())
	}
}

func TestStatusSupportsEnvironmentTokenWithoutConfigFile(t *testing.T) {
	t.Setenv("GSC_ACCESS_TOKEN", testAccessToken)
	t.Setenv("GSC_SERVICE_ACCOUNT_ID", "environment-service-account")
	t.Setenv("GSC_PROFILE", "")

	var stdout bytes.Buffer
	client := &fakeClient{checkResponse: &samsungauth.TokenStatusResponse{OK: true}}
	dependencies := baseDependencies(t, &stdout, client, &fakeTokenStore{})
	dependencies.LoadConfig = func() (*config.Config, error) {
		return nil, config.ErrNotFound
	}

	err := execute(NewCommand(dependencies), "status", "--output", "json")
	if err != nil {
		t.Fatalf("status error = %v", err)
	}
	if client.checkCalls != 1 || client.checkToken != testAccessToken {
		t.Fatalf("check calls = %d, token = %q", client.checkCalls, client.checkToken)
	}
	assertNoSecrets(t, stdout.String(), testAccessToken)
	if !strings.Contains(stdout.String(), `"serviceAccountId":"environment-service-account"`) {
		t.Fatalf("stdout = %q", stdout.String())
	}
}

func TestStatusRejectsServiceAccountCredentialsWithoutMinting(t *testing.T) {
	client := &fakeClient{}
	store := &fakeTokenStore{getErr: credentials.ErrTokenNotFound}
	dependencies := baseDependencies(t, &bytes.Buffer{}, client, store)

	err := execute(NewCommand(dependencies), "status", "--profile", "production")
	if err == nil || !strings.Contains(err.Error(), "requires a stored or environment access token") {
		t.Fatalf("error = %v", err)
	}
	if client.checkCalls != 0 || client.exchangeCalls != 0 {
		t.Fatalf("check calls = %d, exchange calls = %d", client.checkCalls, client.exchangeCalls)
	}
}

func TestStatusRedactsTokenFromClientErrors(t *testing.T) {
	client := &fakeClient{checkErr: fmt.Errorf("invalid %s", testAccessToken)}
	store := &fakeTokenStore{getToken: testAccessToken}
	dependencies := baseDependencies(t, &bytes.Buffer{}, client, store)

	err := execute(NewCommand(dependencies), "status", "--profile", "production")
	if err == nil {
		t.Fatal("status error = nil")
	}
	assertNoSecrets(t, err.Error(), testAccessToken)
}

func TestStatusSupportsTableOutput(t *testing.T) {
	var stdout bytes.Buffer
	client := &fakeClient{checkResponse: &samsungauth.TokenStatusResponse{OK: true}}
	dependencies := baseDependencies(t, &stdout, client, &fakeTokenStore{getToken: testAccessToken})

	err := execute(NewCommand(dependencies), "status", "--profile", "production", "--output", "table")
	if err != nil {
		t.Fatalf("status error = %v", err)
	}
	if !strings.Contains(stdout.String(), "PROFILE") || !strings.Contains(stdout.String(), "production") {
		t.Fatalf("stdout = %q", stdout.String())
	}
	assertNoSecrets(t, stdout.String(), testAccessToken)
}

func TestRevokeRequiresConfirmationBeforeCredentialReads(t *testing.T) {
	client := &fakeClient{}
	dependencies := baseDependencies(t, &bytes.Buffer{}, client, &fakeTokenStore{})
	var loadCalls int
	dependencies.LoadConfig = func() (*config.Config, error) {
		loadCalls++
		return nil, errors.New("must not load")
	}

	err := execute(NewCommand(dependencies), "revoke", "--profile", "production")
	if err == nil || !strings.Contains(err.Error(), "explicit confirmation required") {
		t.Fatalf("error = %v", err)
	}
	if loadCalls != 0 || client.revokeCalls != 0 {
		t.Fatalf("load calls = %d, revoke calls = %d", loadCalls, client.revokeCalls)
	}
}

func TestRevokeDryRunReadsExactProfileWithoutMutating(t *testing.T) {
	var stdout bytes.Buffer
	client := &fakeClient{}
	store := &fakeTokenStore{getToken: testAccessToken}
	dependencies := baseDependencies(t, &stdout, client, store)

	err := execute(
		NewCommand(dependencies),
		"revoke",
		"--profile", "production",
		"--dry-run",
		"--output", "json",
	)
	if err != nil {
		t.Fatalf("revoke dry-run error = %v", err)
	}
	if client.revokeCalls != 0 || store.deleteCalls != 0 {
		t.Fatalf("revoke calls = %d, delete calls = %d", client.revokeCalls, store.deleteCalls)
	}
	assertNoSecrets(t, stdout.String(), testAccessToken)
	if !strings.Contains(stdout.String(), `"mutationsPerformed":false`) {
		t.Fatalf("stdout = %q", stdout.String())
	}
}

func TestRevokeRemoteFailureNeverDeletesStoredToken(t *testing.T) {
	client := &fakeClient{revokeErr: fmt.Errorf("failed for %s", testAccessToken)}
	store := &fakeTokenStore{getToken: testAccessToken}
	dependencies := baseDependencies(t, &bytes.Buffer{}, client, store)

	err := execute(NewCommand(dependencies), "revoke", "--profile", "production", "--confirm")
	if err == nil {
		t.Fatal("revoke error = nil")
	}
	assertNoSecrets(t, err.Error(), testAccessToken)
	if store.deleteCalls != 0 {
		t.Fatalf("delete calls = %d, want 0", store.deleteCalls)
	}
}

func TestRevokeDeletesLocalTokenOnlyAfterRemoteSuccess(t *testing.T) {
	var stdout bytes.Buffer
	var events []string
	client := &fakeClient{
		revokeResponse: &samsungauth.TokenStatusResponse{OK: true},
		onRevoke: func() {
			events = append(events, "remote revoke")
		},
	}
	store := &fakeTokenStore{
		getToken: testAccessToken,
		onDelete: func() {
			events = append(events, "local delete")
		},
	}
	dependencies := baseDependencies(t, &stdout, client, store)

	err := execute(
		NewCommand(dependencies),
		"revoke",
		"--profile", "production",
		"--confirm",
		"--output", "json",
	)
	if err != nil {
		t.Fatalf("revoke error = %v", err)
	}
	if fmt.Sprint(events) != "[remote revoke local delete]" {
		t.Fatalf("events = %v", events)
	}
	assertNoSecrets(t, stdout.String(), testAccessToken)
	if !strings.Contains(stdout.String(), `"revoked":true`) ||
		!strings.Contains(stdout.String(), `"tokenDeleted":true`) {
		t.Fatalf("stdout = %q", stdout.String())
	}
}

func TestRevokeDoesNotReportSuccessWhenLocalDeletionFails(t *testing.T) {
	var stdout bytes.Buffer
	client := &fakeClient{revokeResponse: &samsungauth.TokenStatusResponse{OK: true}}
	store := &fakeTokenStore{
		getToken:  testAccessToken,
		deleteErr: fmt.Errorf("delete failed for %s", testAccessToken),
	}
	dependencies := baseDependencies(t, &stdout, client, store)

	err := execute(NewCommand(dependencies), "revoke", "--profile", "production", "--confirm")
	if err == nil {
		t.Fatal("revoke error = nil")
	}
	assertNoSecrets(t, err.Error(), testAccessToken)
	if stdout.Len() != 0 {
		t.Fatalf("stdout = %q, want no success output", stdout.String())
	}
}

func TestInvalidOutputIsRejectedBeforeAnyDependencySideEffect(t *testing.T) {
	dependencies := baseDependencies(t, &bytes.Buffer{}, &fakeClient{}, &fakeTokenStore{})
	var loadCalls int
	dependencies.LoadConfig = func() (*config.Config, error) {
		loadCalls++
		return nil, errors.New("must not load")
	}

	err := execute(
		NewCommand(dependencies),
		"status",
		"--output", "xml",
	)
	if err == nil || !strings.Contains(err.Error(), "invalid output format") {
		t.Fatalf("error = %v", err)
	}
	if loadCalls != 0 {
		t.Fatalf("load calls = %d", loadCalls)
	}
}

func TestSafeErrorRedactsDisplayAndPreservesErrorIdentity(t *testing.T) {
	sentinel := errors.New("request failed with secret-access-token")
	safe := safeError(sentinel, "secret-access-token")
	if strings.Contains(safe.Error(), "secret-access-token") {
		t.Fatalf("safe error leaked token: %v", safe)
	}
	if !errors.Is(safe, sentinel) {
		t.Fatal("safe error did not preserve wrapped identity")
	}
}

func baseDependencies(
	t *testing.T,
	stdout *bytes.Buffer,
	client *fakeClient,
	store *fakeTokenStore,
) Dependencies {
	t.Helper()
	cfg := &config.Config{
		DefaultProfile: "production",
		Profiles: map[string]config.Profile{
			"production": {
				ServiceAccountID: "service-account",
				PrivateKeyPath:   "/private/seller-key.pem",
				Scopes:           []string{"publishing"},
			},
		},
	}
	return Dependencies{
		Stderr:  &bytes.Buffer{},
		Printer: output.NewPrinter(stdout, func(io.Writer) bool { return false }),
		Client:  client,
		LoadConfig: func() (*config.Config, error) {
			return cloneConfig(cfg), nil
		},
		SaveConfig: func(*config.Config) error {
			return nil
		},
		OpenTokenStore: func() (credentials.TokenStore, error) {
			return store, nil
		},
		ResolveCredentials: credentials.ResolveFromConfigWithStore,
		LoadPrivateKey: func(string) (*rsa.PrivateKey, error) {
			return testPrivateKey(t), nil
		},
		SignJWT: func(samsungauth.JWTConfig) (string, error) {
			return testJWT, nil
		},
		Now: func() time.Time {
			return time.Date(2026, time.July, 30, 12, 0, 0, 0, time.UTC)
		},
	}
}

func executeLogin(dependencies Dependencies) error {
	return execute(
		NewCommand(dependencies),
		"login",
		"--profile", "production",
		"--service-account-id", "service-account",
		"--private-key", "./seller-key.pem",
		"--scope", "publishing",
	)
}

func execute(command *ffcli.Command, args ...string) error {
	if err := command.Parse(args); err != nil {
		return err
	}
	return command.Run(context.Background())
}

func accessTokenResponse() *samsungauth.AccessTokenResponse {
	return &samsungauth.AccessTokenResponse{
		OK: true,
		CreatedItem: samsungauth.CreatedAccessTokenItem{
			AccessToken: testAccessToken,
		},
	}
}

func assertNoSecrets(t *testing.T, value string, secrets ...string) {
	t.Helper()
	for _, secret := range secrets {
		if secret != "" && strings.Contains(value, secret) {
			t.Fatalf("value leaked secret %q: %s", secret, value)
		}
	}
}

type fakeClient struct {
	exchangeResponse *samsungauth.AccessTokenResponse
	exchangeErr      error
	exchangeCalls    int
	exchangeJWT      string

	checkResponse *samsungauth.TokenStatusResponse
	checkErr      error
	checkCalls    int
	checkToken    string

	revokeResponse *samsungauth.TokenStatusResponse
	revokeErr      error
	revokeCalls    int
	revokeToken    string
	onRevoke       func()
}

func (client *fakeClient) Exchange(_ context.Context, stringJWT string) (*samsungauth.AccessTokenResponse, error) {
	client.exchangeCalls++
	client.exchangeJWT = stringJWT
	return client.exchangeResponse, client.exchangeErr
}

func (client *fakeClient) Check(_ context.Context, _ string, accessToken string) (*samsungauth.TokenStatusResponse, error) {
	client.checkCalls++
	client.checkToken = accessToken
	return client.checkResponse, client.checkErr
}

func (client *fakeClient) Revoke(_ context.Context, _ string, accessToken string) (*samsungauth.TokenStatusResponse, error) {
	client.revokeCalls++
	client.revokeToken = accessToken
	if client.onRevoke != nil {
		client.onRevoke()
	}
	return client.revokeResponse, client.revokeErr
}

type fakeTokenStore struct {
	getToken string
	getErr   error

	setCalls   int
	setProfile string
	setToken   string
	setTokens  []string
	setErr     error

	deleteCalls int
	deleteErr   error
	onDelete    func()
}

func (store *fakeTokenStore) Get(string) (string, error) {
	return store.getToken, store.getErr
}

func (store *fakeTokenStore) Set(profile string, accessToken string) error {
	store.setCalls++
	store.setProfile = profile
	store.setToken = accessToken
	store.setTokens = append(store.setTokens, accessToken)
	return store.setErr
}

func (store *fakeTokenStore) Delete(string) error {
	store.deleteCalls++
	if store.onDelete != nil {
		store.onDelete()
	}
	return store.deleteErr
}
