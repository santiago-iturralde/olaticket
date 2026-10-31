package cloudinary

import (
	"context"
	"crypto/sha1"
	"encoding/hex"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestParseURL(t *testing.T) {
	cfg, err := ParseURL("cloudinary://123456:s3cr3t@demo-cloud")
	if err != nil {
		t.Fatal(err)
	}
	if cfg != (Config{CloudName: "demo-cloud", APIKey: "123456", APISecret: "s3cr3t"}) {
		t.Fatalf("config inesperada: %+v", cfg)
	}

	if cfg, err := ParseURL(""); err != nil || NewUploader(cfg).Enabled() {
		t.Fatalf("vacía debería deshabilitar sin error: %+v %v", cfg, err)
	}
	for _, raw := range []string{"https://123:abc@demo", "cloudinary://123@demo", "cloudinary://123:abc@"} {
		if _, err := ParseURL(raw); err == nil {
			t.Errorf("%q: se esperaba error", raw)
		}
	}
}

func TestUploadSignsRequest(t *testing.T) {
	const secret = "s3cr3t"
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1_1/demo/image/upload" {
			t.Errorf("path = %s", r.URL.Path)
		}
		if err := r.ParseMultipartForm(1 << 20); err != nil {
			t.Fatal(err)
		}
		sum := sha1.Sum([]byte("folder=" + Folder + "&timestamp=1700000000" + secret))
		if got, want := r.FormValue("signature"), hex.EncodeToString(sum[:]); got != want {
			t.Errorf("signature = %s, want %s", got, want)
		}
		if r.FormValue("api_key") != "123" {
			t.Errorf("api_key = %s", r.FormValue("api_key"))
		}
		if _, header, err := r.FormFile("file"); err != nil || header.Filename != "portada.png" {
			t.Errorf("file: %v", err)
		}
		w.Write([]byte(`{"secure_url":"https://res.cloudinary.com/demo/image/upload/v1/olaticket/eventos/x.png"}`))
	}))
	defer server.Close()

	u := NewUploader(Config{CloudName: "demo", APIKey: "123", APISecret: secret})
	u.apiURL = server.URL
	u.now = func() time.Time { return time.Unix(1700000000, 0) }

	got, err := u.Upload(context.Background(), strings.NewReader("png"), "portada.png")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(got, "https://res.cloudinary.com/") {
		t.Fatalf("url = %s", got)
	}
}

func TestUploadReportsCloudinaryError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		w.Write([]byte(`{"error":{"message":"Invalid Signature"}}`))
	}))
	defer server.Close()

	u := NewUploader(Config{CloudName: "demo", APIKey: "123", APISecret: "x"})
	u.apiURL = server.URL
	if _, err := u.Upload(context.Background(), strings.NewReader("png"), "a.png"); err == nil || !strings.Contains(err.Error(), "Invalid Signature") {
		t.Fatalf("err = %v", err)
	}
}
