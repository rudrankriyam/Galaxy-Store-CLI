package content

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime/multipart"
	"os"
	"path/filepath"
	"strings"
)

// UploadResult identifies a file stored in Samsung's temporary upload service.
type UploadResult struct {
	FileKey   string          `json:"fileKey"`
	FileName  string          `json:"fileName"`
	FileSize  string          `json:"fileSize"`
	ErrorCode json.RawMessage `json:"errorCode"`
	ErrorMsg  string          `json:"errorMsg"`
}

// Upload streams one regular, non-symlink file to Samsung. It intentionally
// uses an io.Pipe so APKs and AABs are not buffered in memory.
func (service *Service) Upload(
	ctx context.Context,
	sessionID string,
	path string,
) (*UploadResult, error) {
	if err := requireOpaqueValue("upload session ID", sessionID); err != nil {
		return nil, err
	}
	if strings.TrimSpace(path) == "" {
		return nil, errors.New("upload file path is required")
	}

	file, err := openRegularFile(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()

	reader, writer := io.Pipe()
	multipartWriter := multipart.NewWriter(writer)
	writeResult := make(chan error, 1)
	go func() {
		writeResult <- writeMultipartFile(
			multipartWriter,
			writer,
			file,
			filepath.Base(path),
			sessionID,
		)
	}()

	request, err := service.client.NewUploadRequest(
		ctx,
		reader,
		multipartWriter.FormDataContentType(),
	)
	if err != nil {
		_ = reader.Close()
		<-writeResult
		return nil, fmt.Errorf("build Galaxy Store upload: %w", err)
	}

	var result UploadResult
	_, requestErr := service.client.Do(request, &result)
	_ = reader.Close()
	writeErr := <-writeResult
	if requestErr != nil {
		return nil, fmt.Errorf("upload Galaxy Store file: %w", requestErr)
	}
	if writeErr != nil {
		return nil, fmt.Errorf("stream Galaxy Store file: %w", writeErr)
	}
	if err := result.validate(); err != nil {
		return nil, err
	}
	return &result, nil
}

func openRegularFile(path string) (*os.File, error) {
	linkInfo, err := os.Lstat(path)
	if err != nil {
		return nil, fmt.Errorf("inspect upload file: %w", err)
	}
	if linkInfo.Mode()&os.ModeSymlink != 0 {
		return nil, errors.New("upload file must not be a symbolic link")
	}
	if !linkInfo.Mode().IsRegular() {
		return nil, errors.New("upload file must be a regular file")
	}

	file, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("open upload file: %w", err)
	}
	fileInfo, err := file.Stat()
	if err != nil {
		_ = file.Close()
		return nil, fmt.Errorf("inspect open upload file: %w", err)
	}
	if !fileInfo.Mode().IsRegular() || !os.SameFile(linkInfo, fileInfo) {
		_ = file.Close()
		return nil, errors.New("upload file changed while it was being opened")
	}
	return file, nil
}

func writeMultipartFile(
	multipartWriter *multipart.Writer,
	pipeWriter *io.PipeWriter,
	file *os.File,
	fileName string,
	sessionID string,
) error {
	filePart, err := multipartWriter.CreateFormFile("file", fileName)
	if err != nil {
		_ = pipeWriter.CloseWithError(err)
		return err
	}
	if _, err := io.Copy(filePart, file); err != nil {
		_ = pipeWriter.CloseWithError(err)
		return err
	}
	if err := multipartWriter.WriteField("sessionId", sessionID); err != nil {
		_ = pipeWriter.CloseWithError(err)
		return err
	}
	if err := multipartWriter.Close(); err != nil {
		_ = pipeWriter.CloseWithError(err)
		return err
	}
	return pipeWriter.Close()
}

func (result UploadResult) validate() error {
	if code := bytes.TrimSpace(result.ErrorCode); len(code) != 0 && !bytes.Equal(code, []byte("null")) {
		var display string
		if err := json.Unmarshal(code, &display); err != nil {
			display = string(code)
		}
		if result.ErrorMsg == "" {
			return fmt.Errorf("upload Galaxy Store file: Samsung returned error %s", display)
		}
		return fmt.Errorf(
			"upload Galaxy Store file: Samsung returned error %s: %s",
			display,
			result.ErrorMsg,
		)
	}
	if strings.TrimSpace(result.FileKey) == "" {
		return errors.New("upload Galaxy Store file: Samsung returned no file key")
	}
	return nil
}
