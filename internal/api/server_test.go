package api

import (
	"bytes"
	"mime/multipart"
	"net/http/httptest"
	"testing"
)

func TestOversizedMultipartRejected(t *testing.T) {
	// A small configured limit exercises the same bound without allocating 512 MiB.
	app := New(nil, nil, t.TempDir(), nil)
	app.maxUpload = 32
	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	part, err := writer.CreateFormFile("file", "large.mp4")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = part.Write(bytes.Repeat([]byte("x"), 2<<20)); err != nil {
		t.Fatal(err)
	}
	if err = writer.Close(); err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest("POST", "/api/videos", &body)
	request.Header.Set("Content-Type", writer.FormDataContentType())
	response := httptest.NewRecorder()
	app.Handler().ServeHTTP(response, request)
	if response.Code != 413 {
		t.Fatalf("expected 413, got %d: %s", response.Code, response.Body.String())
	}
}
